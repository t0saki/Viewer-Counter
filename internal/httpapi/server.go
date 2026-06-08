// Package httpapi wires the HTTP router, middleware, public/admin handlers and
// the embedded dashboard.
package httpapi

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"viewer-counter/internal/auth"
	"viewer-counter/internal/config"
	"viewer-counter/internal/counter"
	"viewer-counter/internal/dedup"
	"viewer-counter/internal/privacy"
	"viewer-counter/internal/ratelimit"
	"viewer-counter/internal/store"
	"viewer-counter/web"
)

type Server struct {
	cfg     config.Config
	agg     *counter.Aggregator
	store   *store.Store
	priv    *privacy.Privacy
	dedup   *dedup.Dedup
	auth    *auth.Authenticator
	limiter *ratelimit.Limiter
	logger  *slog.Logger

	originSet       map[string]bool
	allowAllOrigins bool
}

func NewServer(cfg config.Config, agg *counter.Aggregator, st *store.Store, priv *privacy.Privacy,
	dd *dedup.Dedup, authn *auth.Authenticator, lim *ratelimit.Limiter, logger *slog.Logger) *Server {
	set := make(map[string]bool, len(cfg.CORS.AllowedOrigins))
	allowAll := false
	for _, o := range cfg.CORS.AllowedOrigins {
		if o == "*" {
			allowAll = true
		}
		set[o] = true
	}
	return &Server{
		cfg: cfg, agg: agg, store: st, priv: priv, dedup: dd,
		auth: authn, limiter: lim, logger: logger,
		originSet: set, allowAllOrigins: allowAll,
	}
}

// Handler builds the fully-wired HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	cors := corsMiddleware(s.cfg.CORS.AllowedOrigins)
	rl := rateLimitMiddleware(s.limiter, s.cfg.Server.TrustProxy, s.cfg.Server.RealIPHeaders)
	maxBytes := maxBytesMiddleware(s.cfg.Server.MaxBodyBytes)
	authn := authMiddleware(s.auth)

	public := func(h http.HandlerFunc) http.Handler {
		return chain(h, cors, rl, maxBytes)
	}
	admin := func(h http.HandlerFunc) http.Handler {
		return chain(h, authn, maxBytes)
	}

	// Public endpoints (no auth).
	mux.Handle("/api/v1/hit", public(s.handleHit)) // GET/POST/OPTIONS
	mux.Handle("/pixel.gif", public(s.handlePixel))
	// No method prefix: OPTIONS must reach corsMiddleware so the preflight is
	// answered with 204 + CORS headers. A "GET ..." pattern makes ServeMux
	// reject OPTIONS with 405 before the middleware runs. Method is enforced
	// inside the handlers instead.
	mux.Handle("/api/v1/count", public(s.handleCount))   // GET/OPTIONS
	mux.Handle("/api/v1/recent", public(s.handleRecent)) // GET/OPTIONS

	// Admin endpoints (bearer token).
	mux.Handle("GET /api/v1/admin/pages", admin(s.handlePages))
	mux.Handle("GET /api/v1/admin/timeseries", admin(s.handleTimeseries))
	mux.Handle("GET /api/v1/admin/by-ip", admin(s.handleByIP))
	mux.Handle("GET /api/v1/admin/events", admin(s.handleEvents))

	// Ops.
	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Dashboard (static, embedded).
	if sub, err := fs.Sub(web.Dashboard, "dashboard"); err == nil {
		fileServer := http.FileServerFS(sub)
		mux.Handle("/dashboard/", http.StripPrefix("/dashboard/", fileServer))
		mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/dashboard/", http.StatusFound)
		})
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/dashboard/", http.StatusFound)
		})
	}

	return chain(mux, recoverMiddleware(s.logger))
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func parseInt(s string, def, lo, hi int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// parseTimeParam accepts RFC3339 or a unix-seconds integer, returning def on
// empty/invalid input.
func parseTimeParam(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(sec, 0).UTC()
	}
	return def
}

func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
