package config

import (
	"testing"
	"time"
)

func TestApplyEnvOverrides(t *testing.T) {
	t.Setenv("VC_DB_DSN", "user:pass@tcp(h:3306)/db")
	t.Setenv("VC_PRIVACY_IP_MODE", "none")
	t.Setenv("VC_SERVER_TRUST_PROXY", "true")
	t.Setenv("VC_SERVER_REAL_IP_HEADERS", "CF-Connecting-IP, X-Real-IP")
	t.Setenv("VC_RATE_LIMIT_ENABLED", "false")
	t.Setenv("VC_RATE_LIMIT_RPS", "12.5")
	t.Setenv("VC_DEDUP_WINDOW", "15m")
	t.Setenv("VC_FLUSH_BATCH", "1000")
	t.Setenv("VC_SERVER_MAX_BODY_BYTES", "4096")
	t.Setenv("VC_AUTH_ADMIN_TOKENS", "a,b")
	t.Setenv("VC_CORS_ALLOWED_ORIGINS", "https://x.com,https://y.com")

	cfg, err := Load("") // no file: env over defaults
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB.DSN != "user:pass@tcp(h:3306)/db" {
		t.Errorf("dsn = %q", cfg.DB.DSN)
	}
	if cfg.Privacy.IPMode != "none" {
		t.Errorf("ip_mode = %q", cfg.Privacy.IPMode)
	}
	if !cfg.Server.TrustProxy {
		t.Error("trust_proxy should be true")
	}
	if cfg.RateLimit.Enabled {
		t.Error("rate_limit.enabled should be false (bool override of default true)")
	}
	if cfg.RateLimit.RPS != 12.5 {
		t.Errorf("rps = %v", cfg.RateLimit.RPS)
	}
	if cfg.Dedup.Window.Std() != 15*time.Minute {
		t.Errorf("dedup window = %v", cfg.Dedup.Window.Std())
	}
	if cfg.Flush.Batch != 1000 {
		t.Errorf("flush batch = %d", cfg.Flush.Batch)
	}
	if cfg.Server.MaxBodyBytes != 4096 {
		t.Errorf("max_body_bytes = %d", cfg.Server.MaxBodyBytes)
	}
	if len(cfg.Server.RealIPHeaders) != 2 || cfg.Server.RealIPHeaders[0] != "CF-Connecting-IP" {
		t.Errorf("real_ip_headers = %v", cfg.Server.RealIPHeaders)
	}
	if len(cfg.Auth.AdminTokens) != 2 {
		t.Errorf("admin_tokens = %v", cfg.Auth.AdminTokens)
	}
	if len(cfg.CORS.AllowedOrigins) != 2 {
		t.Errorf("allowed_origins = %v", cfg.CORS.AllowedOrigins)
	}
}

func TestApplyEnvInvalidValue(t *testing.T) {
	t.Setenv("VC_DB_DSN", "dsn")
	t.Setenv("VC_PRIVACY_IP_MODE", "none")
	t.Setenv("VC_FLUSH_INTERVAL", "notaduration")
	if _, err := Load(""); err == nil {
		t.Fatal("expected error for invalid duration env var")
	}
}

func TestSaltAliasPrecedence(t *testing.T) {
	t.Setenv("VC_DB_DSN", "dsn")
	t.Setenv("VC_IP_SALT", "legacy")
	t.Setenv("VC_PRIVACY_SALT", "canonical")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Privacy.Salt != "canonical" {
		t.Errorf("salt = %q, want canonical (VC_PRIVACY_SALT should win)", cfg.Privacy.Salt)
	}
}
