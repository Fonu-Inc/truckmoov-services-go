package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func WithLogging(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http_request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}

// WithAPIKey rejects requests to the wrapped handler if the configured key is
// non-empty and neither the X-Api-Key header nor the api_key query param match.
func WithAPIKey(next http.Handler, apiKey string) http.Handler {
	if apiKey == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Api-Key")
		if key == "" {
			key = r.URL.Query().Get("api_key")
		}
		if key != apiKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid or missing api key"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func WithCORS(next http.Handler, allowedOrigins []string) http.Handler {
	normalizedOrigins := normalizeOrigins(allowedOrigins)
	allowAllOrigins := normalizedOrigins["*"]

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		isAllowedOrigin := origin != "" && (allowAllOrigins || normalizedOrigins[origin])

		if isAllowedOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET,HEAD,PUT,PATCH,POST,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,Idempotency-Key,x-user-role")
			w.Header().Add("Vary", "Origin")
		}

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func normalizeOrigins(origins []string) map[string]bool {
	normalized := map[string]bool{}
	for _, origin := range origins {
		candidate := strings.TrimSpace(origin)
		if candidate == "" {
			continue
		}
		normalized[candidate] = true
	}

	if len(normalized) == 0 {
		normalized["http://localhost:5173"] = true
		normalized["http://127.0.0.1:5173"] = true
	}

	return normalized
}

func Run(ctx context.Context, addr string, handler http.Handler, log *slog.Logger) error {
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Info("http_server_starting", "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
