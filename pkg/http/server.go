package httpx

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
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

// WithAPIKey rejects requests unless the X-Api-Key header carries apiKey. With no
// key configured it refuses everything, unless allowOpen says the endpoint may
// run unguarded (development only).
func WithAPIKey(next http.Handler, apiKey string, allowOpen bool) http.Handler {
	if apiKey == "" {
		if allowOpen {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, http.StatusServiceUnavailable, "api key is not configured")
		})
	}
	expected := []byte(apiKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Api-Key")), expected) != 1 {
			WriteError(w, http.StatusUnauthorized, "invalid or missing api key")
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

type originalBodyKey struct{}

// WithMaxBodyBytes caps the size of request bodies read by downstream handlers.
// Reads beyond the limit fail, and the wrapped MaxBytesReader writes a 413 hint,
// so handlers that io.ReadAll or json.Decode the body stay bounded.
func WithMaxBodyBytes(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r = r.WithContext(context.WithValue(r.Context(), originalBodyKey{}, r.Body))
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// LimitBody replaces the default body cap for this request. A MaxBytesReader
// stacked on the default one can only lower it, so routes that take larger
// uploads must raise it through here.
func LimitBody(w http.ResponseWriter, r *http.Request, limit int64) {
	body := r.Body
	if original, ok := r.Context().Value(originalBodyKey{}).(io.ReadCloser); ok {
		body = original
	}
	if body != nil {
		r.Body = http.MaxBytesReader(w, body, limit)
	}
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

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Info("http_server_starting", "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	<-drained
	return nil
}
