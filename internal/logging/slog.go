package logging

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type contextKey string

const (
	RequestIDKey contextKey = "request_id"
	UserIDKey    contextKey = "user_id"
)

// WithUserID injects the authenticated user ID into context.
func WithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, UserIDKey, userID)
}

// GetUserID retrieves the authenticated user ID from context.
func GetUserID(ctx context.Context) (uuid.UUID, bool) {
	val := ctx.Value(UserIDKey)
	if val == nil {
		return uuid.Nil, false
	}
	id, ok := val.(uuid.UUID)
	return id, ok
}

// GetRequestID retrieves the request ID from context.
func GetRequestID(ctx context.Context) string {
	if val, ok := ctx.Value(RequestIDKey).(string); ok {
		return val
	}
	return ""
}

// WithRequestID injects the request ID into context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// New constructs a standard structured slog.Logger.
func New(environment string, level slog.Level, writer io.Writer) *slog.Logger {
	options := &slog.HandlerOptions{
		Level:     level,
		AddSource: environment != "production",
	}

	var handler slog.Handler
	if environment == "production" {
		handler = slog.NewJSONHandler(writer, options)
	} else {
		handler = slog.NewTextHandler(writer, options)
	}

	return slog.New(handler)
}

// RequestIDMiddleware generates/extracts request IDs.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", requestID)

		ctx := context.WithValue(r.Context(), RequestIDKey, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// responseWriter captures response metadata for completion logging.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// RequestLoggerMiddleware logs a single record upon request completion.
func RequestLoggerMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rw := &responseWriter{ResponseWriter: w}
			next.ServeHTTP(rw, r)

			duration := time.Since(start)
			requestID := GetRequestID(r.Context())
			var userIDStr string
			if uid, ok := GetUserID(r.Context()); ok {
				userIDStr = uid.String()
			}

			attrs := []slog.Attr{
				slog.String("request_id", requestID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.statusCode),
				slog.Int64("response_bytes", rw.bytesWritten),
				slog.Duration("duration", duration),
				slog.String("remote_ip", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			}

			if userIDStr != "" {
				attrs = append(attrs, slog.String("user_id", userIDStr))
			}

			level := slog.LevelInfo
			if rw.statusCode >= 500 {
				level = slog.LevelError
			} else if rw.statusCode >= 400 {
				level = slog.LevelWarn
			}

			logger.LogAttrs(r.Context(), level, "http request completed", attrs...)
		})
	}
}
