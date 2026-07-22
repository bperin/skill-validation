// Package config loads and validates process configuration without retaining
// provider credentials or credential-file paths.
package config

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Profile string

const (
	ProfileLocal Profile = "local"
	ProfileDev   Profile = "dev"
	ProfileProd  Profile = "prod"
)

type HTTP struct {
	Address         string
	RequestTimeout  time.Duration
	BodyLimit       int64
	ShutdownTimeout time.Duration
}

type OIDC struct {
	Issuers          []string
	Audiences        []string
	ServiceIssuers   []string
	ServiceAudiences []string
}

type Config struct {
	Profile          Profile
	HTTP             HTTP
	OIDC             OIDC
	DatabaseURL      string // a reference/endpoint, never printed verbatim
	LogLevel         string
	SecretRefs       map[string]string
	OIDCClientSecret string // resolved secret, never printed verbatim

	TelemetryExporter string
	TelemetryEndpoint string

	// Legacy fields are kept until the auth capability migrates. They are not
	// used by the Task 02 API composition root.
	AppEnv                      string
	JWTSecret                   string
	JWTIssuer                   string
	JWTAudience                 string
	AccessTokenTTL              time.Duration
	RefreshTokenTTL             time.Duration
	GCSBucket                   string
	GCSSigningServiceAccount    string
	GCSUniformBucketLevelAccess bool
	GCSCredentialsConfigured    bool
	EventarcAudience            string
	EventarcIssuer              string

	// Redis configuration
	RedisAddress            string
	RedisPassword           string
	RedisUser               string
	RedisDB                 int
	RedisUseTLS             bool
	RedisInsecureSkipVerify bool
	RedisTimeout            time.Duration
	RedisMaxRetries         int

	// Pub/Sub configuration
	GCPProjectID         string
	PubSubSubscriptionID string
}

type Summary struct {
	Profile           Profile
	HTTPAddress       string
	RequestTimeout    time.Duration
	BodyLimit         int64
	OIDCIssuers       []string
	OIDCAudiences     []string
	Database          string
	SecretRefs        map[string]string
	OIDCClientSecret  string
	TelemetryExporter string
	TelemetryEndpoint string
	RedisAddress      string
	RedisDB           int
	RedisUseTLS       bool
}

func Load() (*Config, error) {
	if err := loadEnvFile(".env"); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read .env file: %w", err)
	}
	return LoadFrom(os.LookupEnv)
}

// LoadFrom makes environment handling deterministic in tests.
func LoadFrom(lookup func(string) (string, bool)) (*Config, error) {
	get := func(key, fallback string) string {
		if v, ok := lookup(key); ok {
			return v
		}
		return fallback
	}
	profile := Profile(strings.ToLower(get("APP_PROFILE", get("APP_ENV", string(ProfileLocal)))))
	requestTimeout, err := duration(get("HTTP_REQUEST_TIMEOUT", "30s"))
	if err != nil {
		return nil, fmt.Errorf("HTTP_REQUEST_TIMEOUT: %w", err)
	}
	shutdownTimeout, err := duration(get("HTTP_SHUTDOWN_TIMEOUT", "10s"))
	if err != nil {
		return nil, fmt.Errorf("HTTP_SHUTDOWN_TIMEOUT: %w", err)
	}
	bodyLimit, err := positiveInt64(get("HTTP_BODY_LIMIT_BYTES", "1048576"))
	if err != nil {
		return nil, fmt.Errorf("HTTP_BODY_LIMIT_BYTES: %w", err)
	}
	accessTTL, err := duration(get("ACCESS_TOKEN_TTL", "15m"))
	if err != nil {
		return nil, fmt.Errorf("ACCESS_TOKEN_TTL: %w", err)
	}
	refreshTTL, err := duration(get("REFRESH_TOKEN_TTL", "720h"))
	if err != nil {
		return nil, fmt.Errorf("REFRESH_TOKEN_TTL: %w", err)
	}
	redisDB, err := strconv.Atoi(get("REDIS_DB", "0"))
	if err != nil {
		return nil, fmt.Errorf("REDIS_DB: %w", err)
	}
	redisMaxRetries, err := strconv.Atoi(get("REDIS_MAX_RETRIES", "3"))
	if err != nil {
		return nil, fmt.Errorf("REDIS_MAX_RETRIES: %w", err)
	}
	redisTimeout, err := duration(get("REDIS_TIMEOUT", "5s"))
	if err != nil {
		return nil, fmt.Errorf("REDIS_TIMEOUT: %w", err)
	}
	cfg := &Config{
		Profile:     profile,
		HTTP:        HTTP{Address: get("HTTP_ADDRESS", ":8080"), RequestTimeout: requestTimeout, BodyLimit: bodyLimit, ShutdownTimeout: shutdownTimeout},
		OIDC:        OIDC{Issuers: csv(get("OIDC_ISSUERS", "")), Audiences: csv(get("OIDC_AUDIENCES", "")), ServiceIssuers: csv(get("SERVICE_OIDC_ISSUERS", "")), ServiceAudiences: csv(get("SERVICE_OIDC_AUDIENCES", ""))},
		DatabaseURL: get("DATABASE_URL", ""), LogLevel: strings.ToUpper(get("LOG_LEVEL", "INFO")), SecretRefs: secretRefs(lookup),
		TelemetryExporter: get("TELEMETRY_EXPORTER", get("OTEL_TRACES_EXPORTER", "noop")),
		TelemetryEndpoint: get("TELEMETRY_ENDPOINT", get("OTEL_EXPORTER_OTLP_ENDPOINT", "")),
		AppEnv:            string(profile), JWTSecret: get("JWT_SECRET", ""), JWTIssuer: get("JWT_ISSUER", "brianskillco-local"), JWTAudience: get("JWT_AUDIENCE", "brianskillco"), AccessTokenTTL: accessTTL, RefreshTokenTTL: refreshTTL,
		GCSBucket: get("GCS_BUCKET", ""), GCSSigningServiceAccount: get("GCS_SIGNING_SERVICE_ACCOUNT", ""), GCSUniformBucketLevelAccess: get("GCS_UNIFORM_BUCKET_LEVEL_ACCESS", "true") == "true", GCSCredentialsConfigured: get("GCS_CREDENTIALS", "") != "", EventarcAudience: get("EVENTARC_AUDIENCE", ""), EventarcIssuer: get("EVENTARC_ISSUER", ""),
		RedisAddress:            get("REDIS_ADDRESS", "localhost:6379"),
		RedisPassword:           get("REDIS_PASSWORD", ""),
		RedisUser:               get("REDIS_USER", ""),
		RedisDB:                 redisDB,
		RedisUseTLS:             get("REDIS_USE_TLS", "false") == "true",
		RedisInsecureSkipVerify: get("REDIS_INSECURE_SKIP_VERIFY", "false") == "true",
		RedisTimeout:            redisTimeout,
		RedisMaxRetries:         redisMaxRetries,
		GCPProjectID:            get("GCP_PROJECT_ID", get("GOOGLE_CLOUD_PROJECT", "")),
		PubSubSubscriptionID:    get("PUBSUB_SUBSCRIPTION_ID", "content-uploaded-sub"),
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Profile != ProfileLocal && c.Profile != ProfileDev && c.Profile != ProfileProd {
		return fmt.Errorf("APP_PROFILE must be local, dev, or prod")
	}
	if strings.TrimSpace(c.HTTP.Address) == "" || c.HTTP.RequestTimeout <= 0 || c.HTTP.ShutdownTimeout <= 0 || c.HTTP.BodyLimit <= 0 {
		return fmt.Errorf("HTTP configuration must have positive limits and an address")
	}
	if c.LogLevel != "DEBUG" && c.LogLevel != "INFO" && c.LogLevel != "WARN" && c.LogLevel != "ERROR" {
		return fmt.Errorf("LOG_LEVEL is invalid")
	}
	for _, issuer := range append(append([]string{}, c.OIDC.Issuers...), c.OIDC.ServiceIssuers...) {
		if err := validHTTPSURL(issuer); err != nil {
			return fmt.Errorf("OIDC issuer: %w", err)
		}
	}
	if c.GCSBucket != "" && !c.GCSUniformBucketLevelAccess {
		return fmt.Errorf("GCS_UNIFORM_BUCKET_LEVEL_ACCESS must be true when GCS_BUCKET is configured")
	}
	if c.Profile == ProfileProd {
		if len(c.OIDC.Issuers) == 0 || len(c.OIDC.Audiences) == 0 {
			return fmt.Errorf("production requires OIDC_ISSUERS and OIDC_AUDIENCES")
		}
		if c.JWTSecret != "" {
			return fmt.Errorf("production rejects JWT_SECRET; use an external OIDC issuer")
		}
		if c.HTTP.Address == ":8080" {
			return fmt.Errorf("production requires an explicit HTTP_ADDRESS")
		}
		if c.GCSCredentialsConfigured {
			return fmt.Errorf("production rejects GCS_CREDENTIALS; use ADC")
		}
		if c.GCPProjectID == "" {
			return fmt.Errorf("production requires GCP_PROJECT_ID")
		}
		if c.PubSubSubscriptionID == "" {
			return fmt.Errorf("production requires PUBSUB_SUBSCRIPTION_ID")
		}
	}
	return nil
}

func (c Config) DiagnosticSummary() Summary {
	refs := make(map[string]string, len(c.SecretRefs))
	for k, v := range c.SecretRefs {
		refs[k] = v
	}
	oidcSecret := ""
	if c.OIDCClientSecret != "" {
		oidcSecret = "[REDACTED]"
	}
	return Summary{Profile: c.Profile, HTTPAddress: c.HTTP.Address, RequestTimeout: c.HTTP.RequestTimeout, BodyLimit: c.HTTP.BodyLimit, OIDCIssuers: append([]string(nil), c.OIDC.Issuers...), OIDCAudiences: append([]string(nil), c.OIDC.Audiences...), Database: redactDSN(c.DatabaseURL), SecretRefs: refs, OIDCClientSecret: oidcSecret, TelemetryExporter: c.TelemetryExporter, TelemetryEndpoint: c.TelemetryEndpoint, RedisAddress: c.RedisAddress, RedisDB: c.RedisDB, RedisUseTLS: c.RedisUseTLS}
}

func (c *Config) Print() { fmt.Printf("Configuration loaded: %+v\n", c.DiagnosticSummary()) }

func duration(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("must be a positive duration")
	}
	return d, nil
}

func positiveInt64(value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("must be a positive integer")
	}
	return n, nil
}

func csv(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func validHTTPSURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("must be an https URL")
	}
	return nil
}

func secretRefs(lookup func(string) (string, bool)) map[string]string {
	refs := map[string]string{}
	for _, key := range []string{"DATABASE_URL_SECRET_REF", "OIDC_CLIENT_SECRET_REF"} {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" {
			refs[key] = v
		}
	}
	return refs
}

func loadEnvFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	return parseEnv(file, os.LookupEnv, os.Setenv)
}

func parseEnv(r io.Reader, lookup func(string) (string, bool), set func(string, string) error) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid environment line")
		}
		key, val = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(val), "\"'")
		if key == "" {
			return fmt.Errorf("empty environment key")
		}
		if _, exists := lookup(key); !exists {
			if err := set(key, val); err != nil {
				return fmt.Errorf("set %s: %w", key, err)
			}
		}
	}
	return scanner.Err()
}

func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return "[REDACTED]"
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	u.RawQuery = ""
	return u.String()
}
