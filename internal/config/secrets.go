package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrMalformedReference = errors.New("malformed secret reference")
	ErrSecretNotFound     = errors.New("secret not found")
	ErrProviderError      = errors.New("secret provider error")
)

// MalformedReferenceError is returned when a secret reference is invalid.
type MalformedReferenceError struct {
	Reference string
	Err       error
}

func (e *MalformedReferenceError) Error() string {
	return fmt.Sprintf("malformed secret reference %q: %v", e.Reference, e.Err)
}

func (e *MalformedReferenceError) Unwrap() error {
	return ErrMalformedReference
}

// SecretNotFoundError is returned when the secret or version does not exist.
type SecretNotFoundError struct {
	Reference string
	Err       error
}

func (e *SecretNotFoundError) Error() string {
	return fmt.Sprintf("secret not found for reference %q: %v", e.Reference, e.Err)
}

func (e *SecretNotFoundError) Unwrap() error {
	return ErrSecretNotFound
}

// ProviderError is returned when the Secret Manager API returns an error.
type ProviderError struct {
	Reference string
	Err       error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("secret provider error for reference %q: %v", e.Reference, e.Err)
}

func (e *ProviderError) Unwrap() error {
	return ErrProviderError
}

// SecretResolver defines the port for resolving secret references.
type SecretResolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// ParseSecretRef parses a Secret Manager reference string.
// It returns the project, secret, and version.
// If the version is not specified, it defaults to "latest".
func ParseSecretRef(ref string) (project, secret, version string, err error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "projects/") {
		return "", "", "", fmt.Errorf("reference must start with 'projects/': %q", ref)
	}
	parts := strings.Split(ref, "/")
	// Expected: projects/{project}/secrets/{secret} (4 parts)
	// or projects/{project}/secrets/{secret}/versions/{version} (6 parts)
	if len(parts) != 4 && len(parts) != 6 {
		return "", "", "", fmt.Errorf("invalid secret reference format: %q", ref)
	}
	if parts[2] != "secrets" {
		return "", "", "", fmt.Errorf("invalid secret reference format (missing 'secrets' segment): %q", ref)
	}
	project = parts[1]
	secret = parts[3]
	if project == "" {
		return "", "", "", fmt.Errorf("empty project ID in reference: %q", ref)
	}
	if secret == "" {
		return "", "", "", fmt.Errorf("empty secret ID in reference: %q", ref)
	}

	if len(parts) == 6 {
		if parts[4] != "versions" {
			return "", "", "", fmt.Errorf("invalid secret reference format (missing 'versions' segment): %q", ref)
		}
		version = parts[5]
		if version == "" {
			return "", "", "", fmt.Errorf("empty version in reference: %q", ref)
		}
	} else {
		version = "latest"
	}

	return project, secret, version, nil
}

// ResolveSecrets resolves all configured secret references using the provided resolver.
// It updates the corresponding fields in Config with the resolved values.
func (c *Config) ResolveSecrets(ctx context.Context, resolver SecretResolver) error {
	if ref, ok := c.SecretRefs["DATABASE_URL_SECRET_REF"]; ok && ref != "" {
		val, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return fmt.Errorf("resolve DATABASE_URL_SECRET_REF: %w", err)
		}
		c.DatabaseURL = val
	}
	if ref, ok := c.SecretRefs["OIDC_CLIENT_SECRET_REF"]; ok && ref != "" {
		val, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return fmt.Errorf("resolve OIDC_CLIENT_SECRET_REF: %w", err)
		}
		c.OIDCClientSecret = val
	}
	return nil
}

// FakeResolver implements SecretResolver for testing.
type FakeResolver struct {
	mu      sync.RWMutex
	secrets map[string]string
	errors  map[string]error
}

// NewFakeResolver creates a new FakeResolver.
func NewFakeResolver() *FakeResolver {
	return &FakeResolver{
		secrets: make(map[string]string),
		errors:  make(map[string]error),
	}
}

// SetSecret sets a secret value for a reference.
func (f *FakeResolver) SetSecret(ref, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets[ref] = value
}

// SetError sets an error to be returned for a reference.
func (f *FakeResolver) SetError(ref string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors[ref] = err
}

// Resolve resolves a secret reference.
func (f *FakeResolver) Resolve(ctx context.Context, ref string) (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if err, ok := f.errors[ref]; ok {
		return "", err
	}

	val, ok := f.secrets[ref]
	if !ok {
		return "", &SecretNotFoundError{Reference: ref, Err: fmt.Errorf("secret not found in fake")}
	}

	return val, nil
}
