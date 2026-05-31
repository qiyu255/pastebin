package handler

import (
	"log/slog"
	"net/http"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
	size   int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.size += int64(n)
	return n, err
}

// LoggingMiddleware logs HTTP requests with sensitive data redacted.
func LoggingMiddleware(logger *slog.Logger, behindProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ip := clientIP(r, behindProxy)

			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			elapsed := time.Since(start)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"query", redactQuery(r.URL.RawQuery),
				"ip", ip,
				"status", rw.status,
				"resp_size", rw.size,
				"req_size", r.ContentLength,
				"elapsed", elapsed,
				"user_agent", r.UserAgent(),
			)
		})
	}
}

// redactQuery redacts sensitive query parameters.
func redactQuery(raw string) string {
	if raw == "" {
		return ""
	}
	// No sensitive query params in this service; return as-is
	return raw
}
