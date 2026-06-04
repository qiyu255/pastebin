package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"pastebin/internal/model"
)

// contextKey is an unexported type for context keys to prevent collisions.
type contextKey string

const requestIDKey contextKey = "request_id"

// RequestID extracts the request ID from the request context.
func RequestID(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// responseWriter wraps http.ResponseWriter to capture status, size,
// and to record when headers are first written.
type responseWriter struct {
	http.ResponseWriter
	status      int
	size        int64
	wroteHeader bool
	start       time.Time
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.wroteHeader = true
		elapsed := time.Since(rw.start)
		rw.Header().Set("X-Processing-Time", elapsed.String())
		rw.status = code
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.size += int64(n)
	return n, err
}

// generateRequestID creates a random hex-encoded request ID.
func generateRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use timestamp-based ID; extremely unlikely to fail
		return hex.EncodeToString([]byte(time.Now().String())[:8])
	}
	return hex.EncodeToString(b)
}

// Middleware is the top-level middleware. It handles concurrency limiting,
// request ID generation, separate request/response logging, timing,
// and request timeout.
func Middleware(logger *slog.Logger, cfg *model.Config) func(http.Handler) http.Handler {
	// Create semaphore for concurrency limiting
	var sem chan struct{}
	if cfg.MaxConcurrentRequests > 0 {
		sem = make(chan struct{}, cfg.MaxConcurrentRequests)
	}

	// Parse request timeout
	timeout, _ := time.ParseDuration(cfg.RequestTimeout)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Concurrency limiting
			if sem != nil {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				default:
					writeText(w, http.StatusServiceUnavailable, "server too busy")
					return
				}
			}

			// Generate request ID
			reqID := generateRequestID()
			r = r.WithContext(context.WithValue(r.Context(), requestIDKey, reqID))
			w.Header().Set("X-Request-ID", reqID)

			start := time.Now()
			ip := clientIP(r, cfg.BehindProxy)

			// Log incoming request
			logger.Info("request",
				"req_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"query", redactQuery(r.URL.RawQuery),
				"ip", ip,
				"req_size", r.ContentLength,
				"user_agent", r.UserAgent(),
			)

			rw := &responseWriter{
				ResponseWriter: w,
				status:         http.StatusOK,
				start:          start,
			}

			// Apply request timeout
			if timeout > 0 {
				ctx, cancel := context.WithTimeout(r.Context(), timeout)
				defer cancel()
				r = r.WithContext(ctx)

				done := make(chan struct{})
				go func() {
					defer close(done)
					next.ServeHTTP(rw, r)
				}()

				select {
				case <-done:
					// Handler completed within timeout
				case <-ctx.Done():
					// Timeout fired — return 500 to abort the request
					if !rw.wroteHeader {
						writeText(w, http.StatusInternalServerError, "request timeout")
					}
					elapsed := time.Since(start)
					logger.Info("response",
						"req_id", reqID,
						"status", http.StatusInternalServerError,
						"resp_size", 0,
						"elapsed", elapsed,
						"timeout", true,
					)
					return
				}
			} else {
				next.ServeHTTP(rw, r)
			}

			elapsed := time.Since(start)

			// Log response
			logger.Info("response",
				"req_id", reqID,
				"status", rw.status,
				"resp_size", rw.size,
				"elapsed", elapsed,
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
