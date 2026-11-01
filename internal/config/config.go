// Package config loads the Piyo API configuration from environment variables.
//
// Every setting has a development default so `DEV_MODE=true` (the default)
// runs with zero configuration. In production (DEV_MODE=false) the secrets
// ADMIN_TOKEN and HASH_SALT are mandatory and Validate() refuses to start
// without them. See .env.example for a documented list of every variable.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Default values used when the corresponding env var is unset.
const (
	DefaultPort                = 8080
	DefaultDevAdminToken       = "dev-admin-token"
	DefaultDevHashSalt         = "dev-salt"
	DefaultSQLitePath          = "./data/piyo.db"
	DefaultMaxReportsPerDay    = 3
	DefaultRejectsToBlock      = 3
	DefaultRejectWindowDays    = 90
	DefaultBlockDays           = 30
	DefaultMetroBounds         = "-18.05,-63.35,-17.60,-62.95"
	DefaultIPRatePerMinute     = 60
	DefaultManifestDataURL     = "http://localhost:8080/v1/data/lines-v12.json"
	DefaultManifestDataVersion = 12
	DefaultFareBs              = "3.50"
	DefaultMinAppVersion       = "1"
)

// Bounds is a lat/lng bounding box (WGS84 degrees).
type Bounds struct {
	MinLat, MinLng, MaxLat, MaxLng float64
}

// Contains reports whether the point lies inside the box (inclusive).
func (b Bounds) Contains(lat, lng float64) bool {
	return lat >= b.MinLat && lat <= b.MaxLat && lng >= b.MinLng && lng <= b.MaxLng
}

// String renders the bounds in the same "minLat,minLng,maxLat,maxLng" form
// accepted by METRO_BOUNDS.
func (b Bounds) String() string {
	return fmt.Sprintf("%g,%g,%g,%g", b.MinLat, b.MinLng, b.MaxLat, b.MaxLng)
}

// ParseBounds parses "minLat,minLng,maxLat,maxLng".
func ParseBounds(s string) (Bounds, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return Bounds{}, fmt.Errorf("expected 4 comma-separated numbers, got %d", len(parts))
	}
	var vals [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return Bounds{}, fmt.Errorf("component %d: %w", i+1, err)
		}
		vals[i] = f
	}
	b := Bounds{MinLat: vals[0], MinLng: vals[1], MaxLat: vals[2], MaxLng: vals[3]}
	if b.MinLat >= b.MaxLat || b.MinLng >= b.MaxLng {
		return Bounds{}, errors.New("min must be strictly less than max")
	}
	return b, nil
}

// Config is the fully resolved runtime configuration.
type Config struct {
	Port    int
	DevMode bool

	AdminToken string
	HashSalt   string
	SQLitePath string

	MaxReportsPerDay int
	RejectsToBlock   int
	RejectWindowDays int
	BlockDays        int
	MetroBounds      Bounds

	IPRatePerMinute    int
	CORSAllowedOrigins []string

	ManifestDataURL     string
	ManifestDataVersion int
	FareBs              string
	MinAppVersion       string

	V2
}

// Mode returns "dev" or "prod" for the health endpoint and logs.
func (c Config) Mode() string {
	if c.DevMode {
		return "dev"
	}
	return "prod"
}

// Load reads the configuration from the environment. It does not validate;
// callers may override fields (for example from command-line flags) and then
// call Validate.
func Load() (Config, error) {
	var cfg Config
	var err error

	cfg.DevMode, err = envBool("DEV_MODE", true)
	if err != nil {
		return cfg, err
	}
	if cfg.Port, err = envInt("PORT", DefaultPort); err != nil {
		return cfg, err
	}

	cfg.AdminToken = os.Getenv("ADMIN_TOKEN")
	cfg.HashSalt = os.Getenv("HASH_SALT")
	cfg.SQLitePath = envString("SQLITE_PATH", DefaultSQLitePath)

	if cfg.MaxReportsPerDay, err = envInt("MAX_REPORTS_PER_DAY", DefaultMaxReportsPerDay); err != nil {
		return cfg, err
	}
	if cfg.RejectsToBlock, err = envInt("REJECTS_TO_BLOCK", DefaultRejectsToBlock); err != nil {
		return cfg, err
	}
	if cfg.RejectWindowDays, err = envInt("REJECT_WINDOW_DAYS", DefaultRejectWindowDays); err != nil {
		return cfg, err
	}
	if cfg.BlockDays, err = envInt("BLOCK_DAYS", DefaultBlockDays); err != nil {
		return cfg, err
	}
	if cfg.MetroBounds, err = ParseBounds(envString("METRO_BOUNDS", DefaultMetroBounds)); err != nil {
		return cfg, fmt.Errorf("METRO_BOUNDS: %w", err)
	}
	if cfg.IPRatePerMinute, err = envInt("IP_RATE_PER_MINUTE", DefaultIPRatePerMinute); err != nil {
		return cfg, err
	}

	// CORS: "*" in dev, nothing in prod unless configured.
	corsDefault := ""
	if cfg.DevMode {
		corsDefault = "*"
	}
	cfg.CORSAllowedOrigins = splitCSV(envString("CORS_ALLOWED_ORIGINS", corsDefault))

	cfg.ManifestDataURL = envString("MANIFEST_DATA_URL", DefaultManifestDataURL)
	if cfg.ManifestDataVersion, err = envInt("MANIFEST_DATA_VERSION", DefaultManifestDataVersion); err != nil {
		return cfg, err
	}
	cfg.FareBs = envString("FARE_BS", DefaultFareBs)
	cfg.MinAppVersion = envString("MIN_APP_VERSION", DefaultMinAppVersion)

	if err := cfg.loadV2(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate applies dev defaults for secrets and rejects invalid combinations.
func (c *Config) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("PORT must be in 1..65535, got %d", c.Port)
	}
	if c.DevMode {
		if c.AdminToken == "" {
			c.AdminToken = DefaultDevAdminToken
		}
		if c.HashSalt == "" {
			c.HashSalt = DefaultDevHashSalt
		}
	} else {
		if c.AdminToken == "" {
			return errors.New("ADMIN_TOKEN is required when DEV_MODE=false")
		}
		if c.HashSalt == "" {
			return errors.New("HASH_SALT is required when DEV_MODE=false")
		}
	}
	for name, v := range map[string]int{
		"MAX_REPORTS_PER_DAY": c.MaxReportsPerDay,
		"REJECTS_TO_BLOCK":    c.RejectsToBlock,
		"REJECT_WINDOW_DAYS":  c.RejectWindowDays,
		"BLOCK_DAYS":          c.BlockDays,
		"IP_RATE_PER_MINUTE":  c.IPRatePerMinute,
	} {
		if v <= 0 {
			return fmt.Errorf("%s must be > 0, got %d", name, v)
		}
	}
	if c.ManifestDataVersion <= 0 {
		return fmt.Errorf("MANIFEST_DATA_VERSION must be > 0, got %d", c.ManifestDataVersion)
	}
	return c.validateV2()
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s: expected integer, got %q", key, v)
	}
	return n, nil
}

func envBool(key string, def bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return false, fmt.Errorf("%s: expected boolean, got %q", key, v)
	}
	return b, nil
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
