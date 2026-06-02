// Package config loads and validates the service configuration from a YAML
// file, with selected fields overridable via environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that unmarshals from a Go duration string such
// as "24h" or "30m" in YAML.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the value as a standard time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

type Config struct {
	Server      ServerConfig    `yaml:"server"`
	DB          DBConfig        `yaml:"db"`
	Privacy     PrivacyConfig   `yaml:"privacy"`
	Dedup       DedupConfig     `yaml:"dedup"`
	Recent      RecentConfig    `yaml:"recent"`
	CORS        CORSConfig      `yaml:"cors"`
	RateLimit   RateLimitConfig `yaml:"rate_limit"`
	Flush       FlushConfig     `yaml:"flush"`
	Auth        AuthConfig      `yaml:"auth"`
	Bot         BotConfig       `yaml:"bot"`
	Events      EventsConfig    `yaml:"events"`
	ReturnCount bool            `yaml:"return_count"`
}

type ServerConfig struct {
	Addr         string   `yaml:"addr"`
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
	IdleTimeout  Duration `yaml:"idle_timeout"`
	TrustProxy   bool     `yaml:"trust_proxy"`
	MaxBodyBytes int64    `yaml:"max_body_bytes"`
	// RealIPHeaders is the ordered list of proxy-set headers to trust for the
	// client IP when TrustProxy is true. These must be set by your trusted
	// edge (which overwrites any client-supplied value), e.g. CF-Connecting-IP
	// behind Cloudflare or X-Real-IP behind Nginx. The first non-empty header
	// wins. Only meaningful when the app is not directly reachable from the
	// internet (traffic always passes through the proxy).
	RealIPHeaders []string `yaml:"real_ip_headers"`
}

type DBConfig struct {
	DSN             string   `yaml:"dsn"`
	MaxOpenConns    int      `yaml:"max_open_conns"`
	MaxIdleConns    int      `yaml:"max_idle_conns"`
	ConnMaxLifetime Duration `yaml:"conn_max_lifetime"`
}

// PrivacyConfig controls what visitor information is persisted.
type PrivacyConfig struct {
	IPMode   string `yaml:"ip_mode"` // none | hash | truncate | full
	RecordUA bool   `yaml:"record_ua"`
	Salt     string `yaml:"salt"`
}

type DedupConfig struct {
	Enabled bool     `yaml:"enabled"`
	Window  Duration `yaml:"window"`
}

type RecentConfig struct {
	Default Duration `yaml:"default"`
	Max     Duration `yaml:"max"`
}

type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
	EnforceOrigin  bool     `yaml:"enforce_origin"`
}

type RateLimitConfig struct {
	Enabled bool    `yaml:"enabled"`
	RPS     float64 `yaml:"rps"`
	Burst   float64 `yaml:"burst"`
}

type FlushConfig struct {
	Interval Duration `yaml:"interval"`
	Batch    int      `yaml:"batch"`
}

type AuthConfig struct {
	AdminTokens []string `yaml:"admin_tokens"`
}

type BotConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Keywords []string `yaml:"keywords"`
}

type EventsConfig struct {
	Record bool `yaml:"record"`
}

func defaultBotKeywords() []string {
	return []string{
		"bot", "crawl", "spider", "slurp", "mediapartners",
		"facebookexternalhit", "bingpreview", "headlesschrome",
		"python-requests", "curl/", "wget", "go-http-client",
	}
}

// Default returns a Config populated with sensible defaults. YAML and env
// overrides are applied on top of these.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Addr:          ":8080",
			ReadTimeout:   Duration(10 * time.Second),
			WriteTimeout:  Duration(10 * time.Second),
			IdleTimeout:   Duration(60 * time.Second),
			MaxBodyBytes:  8 * 1024,
			RealIPHeaders: []string{"X-Real-IP"},
		},
		DB: DBConfig{
			MaxOpenConns:    25,
			MaxIdleConns:    10,
			ConnMaxLifetime: Duration(5 * time.Minute),
		},
		Privacy:     PrivacyConfig{IPMode: "hash", RecordUA: true},
		Dedup:       DedupConfig{Enabled: true, Window: Duration(30 * time.Minute)},
		Recent:      RecentConfig{Default: Duration(24 * time.Hour), Max: Duration(720 * time.Hour)},
		CORS:        CORSConfig{AllowedOrigins: []string{"*"}, EnforceOrigin: false},
		RateLimit:   RateLimitConfig{Enabled: true, RPS: 5, Burst: 20},
		Flush:       FlushConfig{Interval: Duration(time.Second), Batch: 500},
		Bot:         BotConfig{Enabled: true, Keywords: defaultBotKeywords()},
		Events:      EventsConfig{Record: true},
		ReturnCount: true,
	}
}

// Load reads the config file at path (if it exists) over the defaults, applies
// environment overrides, and validates the result. A missing file is allowed
// (run purely from env vars + defaults). Environment variables override file
// values.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return cfg, fmt.Errorf("parse config: %w", err)
			}
		case !os.IsNotExist(err):
			return cfg, fmt.Errorf("read config: %w", err)
			// IsNotExist: fall through to env + defaults.
		}
	}
	if err := cfg.applyEnv(); err != nil {
		return cfg, err
	}
	return cfg, cfg.validate()
}

// applyEnv overlays every config field from VC_<SECTION>_<FIELD> environment
// variables. Slices accept comma-separated values; durations accept Go
// duration strings (e.g. "30m"); bools accept 1/0/true/false. An env var that
// is not set leaves the existing (file/default) value untouched.
func (c *Config) applyEnv() error {
	e := &envApplier{}

	// server
	e.str("VC_SERVER_ADDR", &c.Server.Addr)
	e.dur("VC_SERVER_READ_TIMEOUT", &c.Server.ReadTimeout)
	e.dur("VC_SERVER_WRITE_TIMEOUT", &c.Server.WriteTimeout)
	e.dur("VC_SERVER_IDLE_TIMEOUT", &c.Server.IdleTimeout)
	e.boolean("VC_SERVER_TRUST_PROXY", &c.Server.TrustProxy)
	e.int64("VC_SERVER_MAX_BODY_BYTES", &c.Server.MaxBodyBytes)
	e.strSlice("VC_SERVER_REAL_IP_HEADERS", &c.Server.RealIPHeaders)

	// db
	e.str("VC_DB_DSN", &c.DB.DSN)
	e.integer("VC_DB_MAX_OPEN_CONNS", &c.DB.MaxOpenConns)
	e.integer("VC_DB_MAX_IDLE_CONNS", &c.DB.MaxIdleConns)
	e.dur("VC_DB_CONN_MAX_LIFETIME", &c.DB.ConnMaxLifetime)

	// privacy
	e.str("VC_PRIVACY_IP_MODE", &c.Privacy.IPMode)
	e.boolean("VC_PRIVACY_RECORD_UA", &c.Privacy.RecordUA)
	e.str("VC_IP_SALT", &c.Privacy.Salt)      // legacy alias
	e.str("VC_PRIVACY_SALT", &c.Privacy.Salt) // canonical (wins if both set)

	// dedup
	e.boolean("VC_DEDUP_ENABLED", &c.Dedup.Enabled)
	e.dur("VC_DEDUP_WINDOW", &c.Dedup.Window)

	// recent
	e.dur("VC_RECENT_DEFAULT", &c.Recent.Default)
	e.dur("VC_RECENT_MAX", &c.Recent.Max)

	// cors
	e.strSlice("VC_CORS_ALLOWED_ORIGINS", &c.CORS.AllowedOrigins)
	e.boolean("VC_CORS_ENFORCE_ORIGIN", &c.CORS.EnforceOrigin)

	// rate_limit
	e.boolean("VC_RATE_LIMIT_ENABLED", &c.RateLimit.Enabled)
	e.float("VC_RATE_LIMIT_RPS", &c.RateLimit.RPS)
	e.float("VC_RATE_LIMIT_BURST", &c.RateLimit.Burst)

	// flush
	e.dur("VC_FLUSH_INTERVAL", &c.Flush.Interval)
	e.integer("VC_FLUSH_BATCH", &c.Flush.Batch)

	// auth
	e.strSlice("VC_ADMIN_TOKEN", &c.Auth.AdminTokens)       // legacy alias
	e.strSlice("VC_AUTH_ADMIN_TOKENS", &c.Auth.AdminTokens) // canonical (wins if both set)

	// bot
	e.boolean("VC_BOT_ENABLED", &c.Bot.Enabled)
	e.strSlice("VC_BOT_KEYWORDS", &c.Bot.Keywords)

	// events
	e.boolean("VC_EVENTS_RECORD", &c.Events.Record)

	// misc
	e.boolean("VC_RETURN_COUNT", &c.ReturnCount)

	return e.err
}

// envApplier overlays config fields from env vars, short-circuiting on the
// first parse error.
type envApplier struct{ err error }

func (e *envApplier) str(key string, dst *string) {
	if e.err != nil {
		return
	}
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}

func (e *envApplier) strSlice(key string, dst *[]string) {
	if e.err != nil {
		return
	}
	if v, ok := os.LookupEnv(key); ok {
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		*dst = out
	}
}

func (e *envApplier) boolean(key string, dst *bool) {
	if e.err != nil {
		return
	}
	if v, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			e.err = fmt.Errorf("%s: invalid bool %q", key, v)
			return
		}
		*dst = b
	}
}

func (e *envApplier) integer(key string, dst *int) {
	if e.err != nil {
		return
	}
	if v, ok := os.LookupEnv(key); ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			e.err = fmt.Errorf("%s: invalid int %q", key, v)
			return
		}
		*dst = n
	}
}

func (e *envApplier) int64(key string, dst *int64) {
	if e.err != nil {
		return
	}
	if v, ok := os.LookupEnv(key); ok {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			e.err = fmt.Errorf("%s: invalid int %q", key, v)
			return
		}
		*dst = n
	}
}

func (e *envApplier) float(key string, dst *float64) {
	if e.err != nil {
		return
	}
	if v, ok := os.LookupEnv(key); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			e.err = fmt.Errorf("%s: invalid float %q", key, v)
			return
		}
		*dst = f
	}
}

func (e *envApplier) dur(key string, dst *Duration) {
	if e.err != nil {
		return
	}
	if v, ok := os.LookupEnv(key); ok {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			e.err = fmt.Errorf("%s: invalid duration %q", key, v)
			return
		}
		*dst = Duration(d)
	}
}

func (c *Config) validate() error {
	switch c.Privacy.IPMode {
	case "none", "hash", "truncate", "full":
	default:
		return fmt.Errorf("invalid privacy.ip_mode %q (want none|hash|truncate|full)", c.Privacy.IPMode)
	}
	if c.DB.DSN == "" {
		return fmt.Errorf("db.dsn is required (set in config or VC_DB_DSN)")
	}
	if c.Privacy.IPMode == "hash" && c.Privacy.Salt == "" {
		return fmt.Errorf("privacy.salt is required when ip_mode=hash")
	}
	if c.Flush.Interval.Std() <= 0 {
		return fmt.Errorf("flush.interval must be > 0")
	}
	return nil
}
