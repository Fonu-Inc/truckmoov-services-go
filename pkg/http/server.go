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

const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultMaxHeaderBytes    = 1 << 20 // 1 MiB
	defaultMaxBodyBytes      = 1 << 20 // 1 MiB
)

// WithMaxBodyBytes caps the size of request bodies read by downstream handlers.
// Reads beyond the limit fail, and the wrapped MaxBytesReader writes a 413 hint,
// so handlers that io.ReadAll or json.Decode the body stay bounded.
func WithMaxBodyBytes(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

func Run(ctx context.Context, addr string, handler http.Handler, log *slog.Logger) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           WithMaxBodyBytes(handler, defaultMaxBodyBytes),
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
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
