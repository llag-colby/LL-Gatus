// Package history is a small, self-contained time-series store for the numeric
// metrics the external collectors push (phone counts, WAN uplinks, AP and client
// counts, and anything else a collector starts sending later).
//
// It deliberately does NOT extend Gatus's own storage.Store, for three reasons:
//
//  1. Every table in the Gatus store is foreign-keyed to endpoints(endpoint_id)
//     ON DELETE CASCADE, and DeleteAllEndpointStatusesNotInKeys runs on every
//     startup. Commenting an endpoint out of config.yaml for a single restart
//     would silently erase all of its history.
//  2. The Gatus sqlite store runs with SetMaxOpenConns(1), so a single expensive
//     aggregate query over months of samples would block every result insert and
//     every dashboard read for its duration.
//  3. Adding a method to the Store interface forces a parallel implementation in
//     the memory store plus a mirrored schema in both specific_sqlite.go and
//     specific_postgres.go, for data that has nothing to do with endpoint results.
//
// So this package owns its own sqlite file (conventionally /data/history.db),
// keyed by a plain endpoint_key TEXT with no foreign keys, and with its own
// retention policy. Nothing in Gatus's schema is touched.
//
// Contract: every exported function is a safe no-op when the database was never
// opened, or when Open failed. Open is the only function that returns an error,
// so a host without a writable /data degrades to "no charts" instead of
// preventing the application from starting. Record never returns an error at
// all: a history write must never fail a collector push.
package history

import (
	"database/sql"
	"sort"
	"sync"
	"time"

	"github.com/TwiN/logr"
	_ "modernc.org/sqlite"
)

const (
	// rawRetention is how long individual samples are kept. Two days covers every
	// window the UI renders at full resolution; beyond that the hourly rollups
	// take over, so keeping raw rows longer would only grow the file.
	rawRetention = 48 * time.Hour

	// rollupRetention is how long the hourly aggregates are kept. Ninety days is
	// three times the longest range the UI offers (30d), leaving room to widen it
	// without losing data that was never recorded.
	rollupRetention = 90 * 24 * time.Hour

	// pruneInterval is how often the background pruner runs. Retention is measured
	// in days, so anything more frequent is wasted work.
	pruneInterval = time.Hour
)

// Resolution values returned by Series.
const (
	ResolutionRaw  = "raw"
	ResolutionHour = "hour"
)

// Concurrency: the connection pool is capped at a single connection, exactly like
// storage/store/sql does under WAL, so sqlite never sees concurrent writers and
// every statement is serialized by the driver. The mutex below therefore does not
// serialize queries; it guards only the package-level handle, so that a reader
// can never observe a half-initialized *sql.DB while Open is running.
var (
	mutex sync.RWMutex
	db    *sql.DB
)

// Series is one metric's points over the requested window, ordered oldest first.
// Timestamps are unix MILLISECONDS to match the convention of api/chart.go's
// ResponseTimeHistory, which the same charting code consumes.
//
// Min and Max are populated only at hourly resolution, where each point is an
// average and the spread within the hour is otherwise invisible. At raw
// resolution they are nil, which marshals to JSON null.
type Series struct {
	Timestamps []int64   `json:"timestamps"`
	Values     []float64 `json:"values"`
	Min        []float64 `json:"min"`
	Max        []float64 `json:"max"`
}

// Open opens (creating it if necessary) the history database at path, creates the
// schema, and starts the background pruner. It is idempotent: calling it again
// once a database is open is a no-op returning nil.
//
// A returned error means history is unavailable, not that the application cannot
// run. The caller is expected to log it and carry on; every other function in
// this package stays a no-op until a subsequent Open succeeds.
func Open(path string) error {
	mutex.Lock()
	defer mutex.Unlock()
	if db != nil {
		return nil
	}
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	if err = handle.Ping(); err != nil {
		_ = handle.Close()
		return err
	}
	// Same pragmas as storage/store/sql: WAL for write throughput, NORMAL syncing
	// because losing the last few seconds of charts on a hard power cut is
	// acceptable, and a single connection to keep WAL from producing "database is
	// locked". There are no foreign keys in this schema, so that pragma is left at
	// its default rather than set for show.
	_, _ = handle.Exec("PRAGMA journal_mode=WAL")
	_, _ = handle.Exec("PRAGMA synchronous=NORMAL")
	handle.SetMaxOpenConns(1)
	if err = createSchema(handle); err != nil {
		_ = handle.Close()
		return err
	}
	db = handle
	go prunePeriodically()
	return nil
}

// createSchema creates the two tables and their indices if they do not exist.
//
// Timestamps are unix-seconds INTEGER columns rather than TIMESTAMP, matching
// hour_unix_timestamp in endpoint_uptimes. It keeps comparisons integral and
// avoids the timezone and scan-format bugs that come with sqlite's textual dates.
func createSchema(handle *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS samples (
			endpoint_key TEXT NOT NULL,
			metric       TEXT NOT NULL,
			ts           INTEGER NOT NULL,
			value        REAL NOT NULL,
			PRIMARY KEY (endpoint_key, metric, ts)
		)`,
		`CREATE TABLE IF NOT EXISTS rollups (
			endpoint_key TEXT NOT NULL,
			metric       TEXT NOT NULL,
			hour         INTEGER NOT NULL,
			n            INTEGER NOT NULL,
			sum          REAL NOT NULL,
			min_value    REAL NOT NULL,
			max_value    REAL NOT NULL,
			PRIMARY KEY (endpoint_key, metric, hour)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_samples_key_metric_ts ON samples (endpoint_key, metric, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_rollups_key_metric_hour ON rollups (endpoint_key, metric, hour)`,
	}
	for _, statement := range statements {
		if _, err := handle.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// get returns the current handle, or nil when history is unavailable.
func get() *sql.DB {
	mutex.RLock()
	defer mutex.RUnlock()
	return db
}

// Record persists one reading per metric: a raw sample plus the corresponding
// hourly rollup, both in a single transaction so a chart can never show a point
// that the rollup does not account for.
//
// It never returns an error. Recording history is strictly a side effect of a
// collector push, and a full disk or a locked database must not turn a healthy
// push into a failure, so problems are logged and dropped.
func Record(endpointKey string, metrics map[string]float64, at time.Time) {
	handle := get()
	if handle == nil || len(endpointKey) == 0 || len(metrics) == 0 {
		return
	}
	timestamp := at.Unix()
	hour := at.Truncate(time.Hour).Unix()
	tx, err := handle.Begin()
	if err != nil {
		logr.Errorf("[history.Record] Failed to begin transaction for key=%s: %s", endpointKey, err.Error())
		return
	}
	for metric, value := range metrics {
		// DO NOTHING rather than DO UPDATE: a duplicate (key, metric, second) only
		// happens when a collector retries a push it already delivered, and ignoring
		// the repeat is what keeps the rollup below from counting it twice.
		result, err := tx.Exec(
			`INSERT INTO samples (endpoint_key, metric, ts, value) VALUES ($1, $2, $3, $4)
			 ON CONFLICT(endpoint_key, metric, ts) DO NOTHING`,
			endpointKey, metric, timestamp, value,
		)
		if err != nil {
			_ = tx.Rollback()
			logr.Errorf("[history.Record] Failed to insert sample for key=%s metric=%s: %s", endpointKey, metric, err.Error())
			return
		}
		if inserted, err := result.RowsAffected(); err == nil && inserted == 0 {
			continue // duplicate push, already aggregated
		}
		_, err = tx.Exec(
			`INSERT INTO rollups (endpoint_key, metric, hour, n, sum, min_value, max_value)
			 VALUES ($1, $2, $3, 1, $4, $5, $6)
			 ON CONFLICT(endpoint_key, metric, hour) DO UPDATE SET
				n = rollups.n + 1,
				sum = rollups.sum + excluded.sum,
				min_value = CASE WHEN excluded.min_value < rollups.min_value THEN excluded.min_value ELSE rollups.min_value END,
				max_value = CASE WHEN excluded.max_value > rollups.max_value THEN excluded.max_value ELSE rollups.max_value END`,
			endpointKey, metric, hour, value, value, value,
		)
		if err != nil {
			_ = tx.Rollback()
			logr.Errorf("[history.Record] Failed to update rollup for key=%s metric=%s: %s", endpointKey, metric, err.Error())
			return
		}
	}
	if err = tx.Commit(); err != nil {
		_ = tx.Rollback()
		logr.Errorf("[history.Record] Failed to commit %d metrics for key=%s: %s", len(metrics), endpointKey, err.Error())
	}
}

// Query returns one entry per metric recorded for endpointKey within
// [from, to], along with the resolution the data was read at.
//
// Resolution is chosen from the start of the window, not its length: if from is
// still inside the raw retention then every point in the window exists as a raw
// sample, so the caller gets one point per push. Otherwise the window reaches
// back past what raw retention keeps and would render as a line that simply stops
// partway, so the hourly rollups are used for the whole window instead.
//
// A key with no history yields an empty map, which is a normal state (nothing has
// been pushed yet) rather than an error.
func Query(endpointKey string, from, to time.Time) (map[string]Series, string) {
	resolution := ResolutionHour
	if !from.Before(time.Now().Add(-rawRetention)) {
		resolution = ResolutionRaw
	}
	series := make(map[string]Series)
	handle := get()
	if handle == nil || len(endpointKey) == 0 {
		return series, resolution
	}
	var rows *sql.Rows
	var err error
	if resolution == ResolutionRaw {
		rows, err = handle.Query(
			`SELECT metric, ts, value FROM samples
			 WHERE endpoint_key = $1 AND ts >= $2 AND ts <= $3
			 ORDER BY metric, ts`,
			endpointKey, from.Unix(), to.Unix(),
		)
	} else {
		rows, err = handle.Query(
			`SELECT metric, hour, sum, n, min_value, max_value FROM rollups
			 WHERE endpoint_key = $1 AND hour >= $2 AND hour <= $3
			 ORDER BY metric, hour`,
			endpointKey, from.Truncate(time.Hour).Unix(), to.Unix(),
		)
	}
	if err != nil {
		logr.Errorf("[history.Series] Failed to query %s series for key=%s: %s", resolution, endpointKey, err.Error())
		return series, resolution
	}
	defer rows.Close()
	for rows.Next() {
		var metric string
		var timestamp int64
		var value, minimum, maximum float64
		if resolution == ResolutionRaw {
			if err = rows.Scan(&metric, &timestamp, &value); err != nil {
				logr.Errorf("[history.Series] Failed to scan sample for key=%s: %s", endpointKey, err.Error())
				return series, resolution
			}
		} else {
			var sum float64
			var n int64
			if err = rows.Scan(&metric, &timestamp, &sum, &n, &minimum, &maximum); err != nil {
				logr.Errorf("[history.Series] Failed to scan rollup for key=%s: %s", endpointKey, err.Error())
				return series, resolution
			}
			if n > 0 {
				value = sum / float64(n)
			}
		}
		entry := series[metric]
		entry.Timestamps = append(entry.Timestamps, timestamp*1000)
		entry.Values = append(entry.Values, value)
		if resolution == ResolutionHour {
			entry.Min = append(entry.Min, minimum)
			entry.Max = append(entry.Max, maximum)
		}
		series[metric] = entry
	}
	if err = rows.Err(); err != nil {
		logr.Errorf("[history.Series] Failed to iterate %s series for key=%s: %s", resolution, endpointKey, err.Error())
	}
	return series, resolution
}

// Metrics returns the distinct metric names known for endpointKey, sorted, so the
// UI can discover what it is able to chart without hardcoding a list that goes
// stale the moment a collector reports a new count.
//
// It returns an empty slice and a nil error when history is unavailable, for the
// same reason an unknown key is not a 404: "nothing to chart" is a state the UI
// renders, not a failure it reports.
func Metrics(endpointKey string) ([]string, error) {
	metrics := make([]string, 0)
	handle := get()
	if handle == nil || len(endpointKey) == 0 {
		return metrics, nil
	}
	// Both tables are consulted because raw samples expire long before the rollups
	// do: a metric that stopped being reported three days ago is still chartable.
	rows, err := handle.Query(
		`SELECT metric FROM samples WHERE endpoint_key = $1
		 UNION
		 SELECT metric FROM rollups WHERE endpoint_key = $2`,
		endpointKey, endpointKey,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var metric string
		if err = rows.Scan(&metric); err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(metrics)
	return metrics, nil
}

// Prune deletes raw samples older than rawRetention and rollups older than
// rollupRetention. It is called hourly by the background pruner and is safe to
// call directly.
func Prune() {
	handle := get()
	if handle == nil {
		return
	}
	now := time.Now()
	if _, err := handle.Exec(`DELETE FROM samples WHERE ts < $1`, now.Add(-rawRetention).Unix()); err != nil {
		logr.Errorf("[history.Prune] Failed to prune samples: %s", err.Error())
	}
	if _, err := handle.Exec(`DELETE FROM rollups WHERE hour < $1`, now.Add(-rollupRetention).Unix()); err != nil {
		logr.Errorf("[history.Prune] Failed to prune rollups: %s", err.Error())
	}
}

// prunePeriodically prunes once at startup, then every pruneInterval. The startup
// pass matters because a container that is restarted more often than the interval
// would otherwise never prune at all.
func prunePeriodically() {
	Prune()
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	for range ticker.C {
		Prune()
	}
}
