package observability

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

// ErrorReporter defines the boundary for reporting unexpected runtime errors.
type ErrorReporter interface {
	Report(ctx context.Context, err error, metadata map[string]string)
}

// NoopErrorReporter is a safe no-op implementation for tests and local use.
type NoopErrorReporter struct{}

// NewNoopErrorReporter creates a new NoopErrorReporter.
func NewNoopErrorReporter() *NoopErrorReporter {
	return &NoopErrorReporter{}
}

// Report does nothing.
func (n *NoopErrorReporter) Report(ctx context.Context, err error, metadata map[string]string) {}

// SlogErrorReporter logs redacted errors using slog.
type SlogErrorReporter struct {
	logger *slog.Logger
}

// NewSlogErrorReporter creates a new SlogErrorReporter.
func NewSlogErrorReporter(logger *slog.Logger) *SlogErrorReporter {
	return &SlogErrorReporter{logger: logger}
}

// Report redacts and logs the error.
func (s *SlogErrorReporter) Report(ctx context.Context, err error, metadata map[string]string) {
	if err == nil {
		return
	}
	redactedErr := NewRedactedError(err)
	redactedMeta := RedactMetadata(metadata)

	attrs := []any{slog.String("error", redactedErr.Error())}
	for k, v := range redactedMeta {
		attrs = append(attrs, slog.String(k, v))
	}
	s.logger.ErrorContext(ctx, "runtime error reported", attrs...)
}

var (
	bearerRegex        = regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9_\-\.\~\+\/]+=*`)
	basicAuthRegex     = regexp.MustCompile(`(?i)basic\s+[a-zA-Z0-9_\-\.\~\+\/]+=*`)
	dbPasswordRegex    = regexp.MustCompile(`(?i)(:[^:@/]+)@`)
	genericSecretRegex = regexp.MustCompile(`(?i)(password|secret|token|key|credential)(["']?\s*[:=]\s*["']?)([a-zA-Z0-9_\-\.\~\+\/=]+)`)
)

// RedactString redacts sensitive information from a string.
func RedactString(s string) string {
	s = bearerRegex.ReplaceAllString(s, "Bearer [REDACTED]")
	s = basicAuthRegex.ReplaceAllString(s, "Basic [REDACTED]")
	s = dbPasswordRegex.ReplaceAllString(s, ":[REDACTED]@")
	s = genericSecretRegex.ReplaceAllString(s, "${1}${2}[REDACTED]")
	return s
}

func isSensitiveKey(k string) bool {
	kl := strings.ToLower(k)
	sensitiveSubstrings := []string{"authorization", "password", "secret", "token", "credential", "apikey", "api_key", "private_key", "secret_key", "encryption_key", "signing_key", "access_key", "auth_key"}
	for _, s := range sensitiveSubstrings {
		if strings.Contains(kl, s) {
			return true
		}
	}
	if kl == "key" {
		return true
	}
	return false
}

// RedactMetadata redacts sensitive keys and values from a metadata map.
func RedactMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	redacted := make(map[string]string, len(metadata))
	for k, v := range metadata {
		if isSensitiveKey(k) {
			redacted[k] = "[REDACTED]"
		} else {
			redacted[k] = RedactString(v)
		}
	}
	return redacted
}

// RedactedError wraps an error and redacts its error message.
type RedactedError struct {
	original error
	msg      string
}

// Error returns the redacted error message.
func (e *RedactedError) Error() string {
	return e.msg
}

// Unwrap returns the original error.
func (e *RedactedError) Unwrap() error {
	return e.original
}

// NewRedactedError returns a new RedactedError.
func NewRedactedError(err error) error {
	if err == nil {
		return nil
	}
	return &RedactedError{
		original: err,
		msg:      RedactString(err.Error()),
	}
}
