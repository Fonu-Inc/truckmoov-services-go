package httpx

import (
	"context"
	"encoding/json"
	"net/http"
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

		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":   ready,
			"service": service,
			"checks":  results,
		})
	}
}
