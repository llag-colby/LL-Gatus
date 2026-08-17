package watchdog

import (
	"context"
	"errors"
	"time"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/gatus/v5/config/endpoint"
	"github.com/TwiN/gatus/v5/metrics"
	"github.com/TwiN/gatus/v5/monitoring"
	"github.com/TwiN/gatus/v5/storage/store"
	"github.com/TwiN/logr"
)

var (
	// ErrWatchdogNotRunning is returned by ExecuteEndpointNow when Monitor() has never been called.
	ErrWatchdogNotRunning = errors.New("watchdog is not running")
	// ErrCheckSkipped is returned by ExecuteEndpointNow when the check could not run (shutting down, or no connectivity).
	ErrCheckSkipped = errors.New("check was skipped (shutting down or no connectivity)")
)

// monitorEndpoint a single endpoint in a loop
func monitorEndpoint(ep *endpoint.Endpoint, cfg *config.Config, extraLabels []string, ctx context.Context) {
	// Run it immediately on start
	executeEndpoint(ep, cfg, extraLabels)
	// Loop for the next executions
	ticker := time.NewTicker(ep.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logr.Warnf("[watchdog.monitorEndpoint] Canceling current execution of group=%s; endpoint=%s; key=%s", ep.Group, ep.Name, ep.Key())
			return
		case <-ticker.C:
			executeEndpoint(ep, cfg, extraLabels)
		}
	}
	// Just in case somebody wandered all the way to here and wonders, "what about ExternalEndpoints?"
	// Alerting is checked every time an external endpoint is pushed to Gatus, so they're not monitored
	// periodically like they are for normal endpoints.
}

// ExecuteEndpointNow runs a single out-of-band check for ep, outside of its
// regular interval, and returns the result. Used by the "Force ping" button on
// the endpoint drill-in (POST /api/v1/endpoints/:key/check).
//
// The result is stored and alerted on exactly like a scheduled check — it IS a
// real check, and pretending otherwise would leave the UI showing a result the
// alerting state machine never saw.
func ExecuteEndpointNow(ep *endpoint.Endpoint, cfg *config.Config) (*endpoint.Result, error) {
	// Monitor() populates the semaphore and context; without it there's nothing
	// to coordinate with (only happens if the watchdog was never started).
	if monitoringSemaphore == nil || ctx == nil {
		return nil, ErrWatchdogNotRunning
	}
	result := executeEndpoint(ep, cfg, cfg.GetUniqueExtraMetricLabels())
	if result == nil {
		return nil, ErrCheckSkipped
	}
	return result, nil
}

// executeEndpoint runs one check and returns its result, or nil if the check was
// skipped (shutdown in progress, or no connectivity).
func executeEndpoint(ep *endpoint.Endpoint, cfg *config.Config, extraLabels []string) *endpoint.Result {
	// Acquire semaphore to limit concurrent endpoint monitoring
	if err := monitoringSemaphore.Acquire(ctx, 1); err != nil {
		// Only fails if context is cancelled (during shutdown)
		logr.Debugf("[watchdog.executeEndpoint] Context cancelled, skipping execution: %s", err.Error())
		return nil
	}
	defer monitoringSemaphore.Release(1)
	// If there's a connectivity checker configured, check if Gatus has internet connectivity
	if cfg.Connectivity != nil && cfg.Connectivity.Checker != nil && !cfg.Connectivity.Checker.IsConnected() {
		logr.Infof("[watchdog.executeEndpoint] No connectivity; skipping execution")
		return nil
	}
	// If monitoring is paused for this endpoint, run no check at all: nothing is
	// stored, no alert fires, no metric is recorded, and the existing history is
	// left untouched so un-pausing resumes the same timeline.
	if monitoring.IsPaused(ep.Key()) {
		logr.Debugf("[watchdog.executeEndpoint] Monitoring paused; skipping execution of group=%s; endpoint=%s; key=%s", ep.Group, ep.Name, ep.Key())
		return nil
	}
	logr.Debugf("[watchdog.executeEndpoint] Monitoring group=%s; endpoint=%s; key=%s", ep.Group, ep.Name, ep.Key())
	result := ep.EvaluateHealth()
	if cfg.Metrics {
		metrics.PublishMetricsForEndpoint(ep, result, extraLabels)
	}
	UpdateEndpointStatus(ep, result)
	if logr.GetThreshold() == logr.LevelDebug && !result.Success {
		logr.Debugf("[watchdog.executeEndpoint] Monitored group=%s; endpoint=%s; key=%s; success=%v; errors=%d; duration=%s; body=%s", ep.Group, ep.Name, ep.Key(), result.Success, len(result.Errors), result.Duration.Round(time.Millisecond), result.Body)
	} else {
		logr.Infof("[watchdog.executeEndpoint] Monitored group=%s; endpoint=%s; key=%s; success=%v; errors=%d; duration=%s", ep.Group, ep.Name, ep.Key(), result.Success, len(result.Errors), result.Duration.Round(time.Millisecond))
	}
	inEndpointMaintenanceWindow := false
	for _, maintenanceWindow := range ep.MaintenanceWindows {
		if maintenanceWindow.IsUnderMaintenance() {
			logr.Debug("[watchdog.executeEndpoint] Under endpoint maintenance window")
			inEndpointMaintenanceWindow = true
		}
	}
	if !cfg.Maintenance.IsUnderMaintenance() && !inEndpointMaintenanceWindow {
		HandleAlerting(ep, result, cfg.Alerting)
	} else {
		logr.Debug("[watchdog.executeEndpoint] Not handling alerting because currently in the maintenance window")
	}
	logr.Debugf("[watchdog.executeEndpoint] Waiting for interval=%s before monitoring group=%s endpoint=%s (key=%s) again", ep.Interval, ep.Group, ep.Name, ep.Key())
	return result
}

// UpdateEndpointStatus persists the endpoint result in the storage
func UpdateEndpointStatus(ep *endpoint.Endpoint, result *endpoint.Result) {
	if err := store.Get().InsertEndpointResult(ep, result); err != nil {
		logr.Errorf("[watchdog.UpdateEndpointStatus] Failed to insert result in storage: %s", err.Error())
	}
}
