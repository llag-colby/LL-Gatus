package monitoring

import (
	"encoding/json"
	"os"
	"sort"
	"sync"

	"github.com/TwiN/logr"
)

// Paused monitoring, per endpoint key. Pausing an endpoint is real, not
// cosmetic: the watchdog skips its checks and heartbeats, collector pushes are
// accepted-but-discarded, and neither alerting nor metrics are touched. Already
// recorded history is deliberately left alone, so un-pausing resumes the same
// timeline instead of starting a new one.
//
// State is persisted to /data (mounted, gitignored) so it survives updates, and
// lazily loaded on first use because /data is only guaranteed to exist at
// runtime.
//
// This package must not import api or watchdog — both of them import it, and
// api already imports watchdog, so either direction would be an import cycle.
var (
	mu     sync.RWMutex
	paused = map[string]bool{}
	loaded bool
)

const pausedPath = "/data/monitoring.json"

type monitoringFile struct {
	Paused []string `json:"paused"`
}

// ensureLoaded reads the persisted state once. A missing or corrupt file simply
// means "nothing is paused" — monitoring everything is the safe default, so
// there's no error to report to the caller.
func ensureLoaded() {
	mu.Lock()
	defer mu.Unlock()
	if loaded {
		return
	}
	loaded = true
	if b, err := os.ReadFile(pausedPath); err == nil {
		var f monitoringFile
		if json.Unmarshal(b, &f) == nil {
			for _, key := range f.Paused {
				paused[key] = true
			}
		}
	}
}

func persist() {
	// caller holds mu
	f := monitoringFile{Paused: sortedKeys()}
	if b, err := json.MarshalIndent(f, "", "  "); err == nil {
		if err := os.WriteFile(pausedPath, b, 0o644); err != nil {
			logr.Errorf("[monitoring.persist] could not write %s: %s", pausedPath, err.Error())
		}
	}
}

// sortedKeys returns the paused keys in a stable order. Caller holds mu.
func sortedKeys() []string {
	keys := make([]string, 0, len(paused))
	for key := range paused {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// IsPaused returns whether monitoring is paused for the given endpoint key.
// Called on every check, so it only takes a read lock.
func IsPaused(key string) bool {
	ensureLoaded()
	mu.RLock()
	defer mu.RUnlock()
	return paused[key]
}

// SetPaused pauses or resumes monitoring for the given endpoint key, persisting
// the change immediately.
func SetPaused(key string, isPaused bool) {
	ensureLoaded()
	mu.Lock()
	defer mu.Unlock()
	if isPaused {
		paused[key] = true
	} else {
		delete(paused, key)
	}
	persist()
}

// Paused returns a sorted copy of every paused endpoint key.
func Paused() []string {
	ensureLoaded()
	mu.RLock()
	defer mu.RUnlock()
	return sortedKeys()
}
