package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"viewer-counter/internal/auth"
	"viewer-counter/internal/config"
	"viewer-counter/internal/counter"
	"viewer-counter/internal/dedup"
	"viewer-counter/internal/privacy"
	"viewer-counter/internal/ratelimit"
	"viewer-counter/internal/store"
)

// discardFlusher satisfies the counter flusher interface without a DB.
type discardFlusher struct{}

func (discardFlusher) FlushCounters(context.Context, map[store.Key]int64) error      { return nil }
func (discardFlusher) FlushBuckets(context.Context, map[store.BucketKey]int64) error { return nil }
func (discardFlusher) InsertEvents(context.Context, []store.Event) error             { return nil }

func keyFor(site, page string) store.Key { return store.Key{Site: site, Page: page} }

func testServer(t *testing.T, cfg config.Config) (*Server, *counter.Aggregator) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	agg := counter.New(discardFlusher{}, cfg.Events.Record, time.Hour, 0, logger)
	priv := privacy.New(cfg.Privacy.IPMode, cfg.Privacy.Salt, cfg.Privacy.RecordUA, cfg.Bot.Enabled, cfg.Bot.Keywords)
	var dd *dedup.Dedup
	if cfg.Dedup.Enabled {
		dd = dedup.New(cfg.Dedup.Window.Std())
	}
	var lim *ratelimit.Limiter
	if cfg.RateLimit.Enabled {
		lim = ratelimit.New(cfg.RateLimit.RPS, cfg.RateLimit.Burst)
	}
	srv := NewServer(cfg, agg, nil, priv, dd, auth.New(cfg.Auth.AdminTokens), lim, logger)
	return srv, agg
}

func baseConfig() config.Config {
	c := config.Default()
	c.RateLimit.Enabled = false
	c.Privacy.Salt = "test-salt"
	return c
}

func hit(h http.Handler, query string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hit?"+query, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHitIncrementsCount(t *testing.T) {
	srv, _ := testServer(t, baseConfig())
	h := srv.Handler()

	for i := 0; i < 3; i++ {
		// Vary UA so dedup does not collapse them.
		rr := hit(h, "site=demo&page=/home", map[string]string{"User-Agent": "ua" + string(rune('a'+i))})
		if rr.Code != http.StatusOK {
			t.Fatalf("hit %d: code = %d, body = %s", i, rr.Code, rr.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/count?site=demo&page=/home", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp struct{ Count int64 }
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Count != 3 {
		t.Fatalf("count = %d, want 3", resp.Count)
	}
}

func TestHitDedup(t *testing.T) {
	srv, agg := testServer(t, baseConfig())
	h := srv.Handler()
	for i := 0; i < 4; i++ {
		hit(h, "site=demo&page=/p", map[string]string{"User-Agent": "same-ua"})
	}
	if got := agg.Total(keyFor("demo", "/p")); got != 1 {
		t.Fatalf("deduped total = %d, want 1", got)
	}
}

func TestHitBotSkipped(t *testing.T) {
	srv, agg := testServer(t, baseConfig())
	h := srv.Handler()
	hit(h, "site=demo&page=/p", map[string]string{"User-Agent": "Googlebot/2.1"})
	if got := agg.Total(keyFor("demo", "/p")); got != 0 {
		t.Fatalf("bot total = %d, want 0", got)
	}
}

func TestHitMissingParams(t *testing.T) {
	srv, _ := testServer(t, baseConfig())
	rr := hit(srv.Handler(), "site=demo", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
}

func TestHitMethodNotAllowed(t *testing.T) {
	srv, _ := testServer(t, baseConfig())
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/hit?site=demo&page=/p", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", rr.Code)
	}
}

func TestOriginEnforcement(t *testing.T) {
	c := baseConfig()
	c.CORS.AllowedOrigins = []string{"https://ok.com"}
	c.CORS.EnforceOrigin = true
	srv, _ := testServer(t, c)
	h := srv.Handler()

	if rr := hit(h, "site=d&page=/p", nil); rr.Code != http.StatusForbidden {
		t.Fatalf("no-origin code = %d, want 403", rr.Code)
	}
	if rr := hit(h, "site=d&page=/p", map[string]string{"Origin": "https://ok.com", "User-Agent": "x"}); rr.Code != http.StatusOK {
		t.Fatalf("allowed-origin code = %d, want 200", rr.Code)
	}
	if rr := hit(h, "site=d&page=/p", map[string]string{"Origin": "https://evil.com", "User-Agent": "y"}); rr.Code != http.StatusForbidden {
		t.Fatalf("bad-origin code = %d, want 403", rr.Code)
	}
}

// TestCountPreflight guards the CORS preflight regression: a method-prefixed
// mux pattern ("GET /api/v1/count") makes ServeMux answer OPTIONS with 405
// before corsMiddleware runs, breaking cross-origin GETs. The preflight must
// short-circuit to 204 with the matching Access-Control-Allow-Origin header.
func TestCountPreflight(t *testing.T) {
	c := baseConfig()
	c.CORS.AllowedOrigins = []string{"https://ok.com"}
	srv, _ := testServer(t, c)
	h := srv.Handler()

	for _, path := range []string{"/api/v1/count", "/api/v1/recent"} {
		req := httptest.NewRequest(http.MethodOptions, path+"?site=d&page=/p", nil)
		req.Header.Set("Origin", "https://ok.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("%s preflight code = %d, want 204", path, rr.Code)
		}
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://ok.com" {
			t.Fatalf("%s allow-origin = %q, want https://ok.com", path, got)
		}
	}
}

func TestRateLimit(t *testing.T) {
	c := baseConfig()
	c.RateLimit.Enabled = true
	c.RateLimit.RPS = 0.0001
	c.RateLimit.Burst = 2
	srv, _ := testServer(t, c)
	h := srv.Handler()

	codes := make([]int, 0, 4)
	for i := 0; i < 4; i++ {
		codes = append(codes, hit(h, "site=d&page=/p", map[string]string{"User-Agent": "ua" + string(rune('a'+i))}).Code)
	}
	// burst=2 -> first two pass, rest limited
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("first two codes = %v, want 200,200", codes[:2])
	}
	if codes[3] != http.StatusTooManyRequests {
		t.Fatalf("4th code = %d, want 429", codes[3])
	}
}

func TestAdminRequiresToken(t *testing.T) {
	c := baseConfig()
	c.Auth.AdminTokens = []string{"secret"}
	srv, _ := testServer(t, c)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/pages", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no-token code = %d, want 401", rr.Code)
	}
}
