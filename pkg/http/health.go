package httpx

import (
	"context"
	"net/http"
	"sort"
	"time"
)

// ReadinessCheck is a single named dependency probe evaluated by the readiness
// handler. Probe should ping the dependency and return a non-nil error when it
// is unreachable.
type ReadinessCheck struct {
	Name  string
	Probe func(ctx context.Context) error
}

const readinessProbeTimeout = 3 * time.Second

// ReadinessHandler evaluates every dependency probe and reports 200 only when
// all pass. If any probe fails it responds 503 with a JSON body naming each
// dependency and, for failures, the error — so an operator (and the k8s
// readiness gate) can see exactly which dependency is down. With no checks it
// always reports ready.
//
// This is the readiness (/ready) probe. Liveness (/health) stays shallow and
// dependency-free so a transient dependency outage never restarts the pod.
func ReadinessHandler(service string, checks ...ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := make(map[string]string, len(checks))
		ready := true

		for _, check := range checks {
			ctx, cancel := context.WithTimeout(r.Context(), readinessProbeTimeout)
			err := check.Probe(ctx)
			cancel()
			if err != nil {
				ready = false
				results[check.Name] = err.Error()
				continue
			}
			results[check.Name] = "ok"
		}

		if !ready {
			WriteError(w, http.StatusServiceUnavailable, "service not ready", failedChecks(results)...)
			return
		}
		WriteOK(w, http.StatusOK, map[string]any{
			"ready":   true,
			"service": service,
			"checks":  results,
		})
	}
}

func failedChecks(results map[string]string) []string {
	failed := make([]string, 0, len(results))
	for name, result := range results {
		if result != "ok" {
			failed = append(failed, name+": "+result)
		}
	}
	sort.Strings(failed)
	return failed
}
