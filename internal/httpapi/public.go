package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"viewer-counter/internal/privacy"
	"viewer-counter/internal/store"
)

// 1x1 transparent GIF.
var pixelGIF = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00,
	0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00,
	0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00,
	0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b,
}

func (s *Server) handleHit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	site, page, ok := s.sitePage(w, r)
	if !ok {
		return
	}
	if !s.checkOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	s.recordHit(r, site, page)
	count := s.agg.Total(store.Key{Site: site, Page: page})
	resp := map[string]any{"ok": true}
	if s.cfg.ReturnCount {
		resp["count"] = count
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePixel(w http.ResponseWriter, r *http.Request) {
	site, page, ok := s.sitePage(w, r)
	if ok && s.checkOrigin(r) {
		s.recordHit(r, site, page)
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pixelGIF)
}

func (s *Server) handleCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	site, page, ok := s.sitePage(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site":  site,
		"page":  page,
		"count": s.agg.Total(store.Key{Site: site, Page: page}),
	})
}

func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	site, page, ok := s.sitePage(w, r)
	if !ok {
		return
	}
	window := s.cfg.Recent.Default.Std()
	if ws := r.FormValue("window"); ws != "" {
		d, err := time.ParseDuration(ws)
		if err != nil || d <= 0 {
			http.Error(w, "invalid window (e.g. 24h, 30m)", http.StatusBadRequest)
			return
		}
		window = d
	}
	if maxW := s.cfg.Recent.Max.Std(); maxW > 0 && window > maxW {
		window = maxW
	}
	since := time.Now().UTC().Add(-window).Truncate(time.Hour)
	count, err := s.store.Recent(r.Context(), site, page, since)
	if err != nil {
		s.logger.Error("recent query failed", "err", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site":   site,
		"page":   page,
		"window": window.String(),
		"count":  count,
	})
}

// recordHit applies bot filtering and dedup, then records the view. Bots and
// duplicates are silently skipped (no increment).
func (s *Server) recordHit(r *http.Request, site, page string) {
	ua := r.UserAgent()
	if s.priv.IsBot(ua) {
		return
	}
	ip := privacy.ClientIP(r, s.cfg.Server.TrustProxy, s.cfg.Server.RealIPHeaders)
	if s.dedup != nil && s.dedup.Seen(privacy.DedupKey(site, page, ip, ua)) {
		return
	}
	s.agg.Record(store.Event{
		Site:    site,
		Page:    page,
		TS:      time.Now().UTC(),
		IPHash:  s.priv.StoreIP(ip),
		UA:      s.priv.StoreUA(ua),
		Referer: truncate(r.Referer(), 512),
	})
}

// checkOrigin enforces the origin allowlist for state-changing requests when
// enforcement is enabled. With a wildcard allowlist or enforcement off, all
// requests pass.
func (s *Server) checkOrigin(r *http.Request) bool {
	if !s.cfg.CORS.EnforceOrigin || s.allowAllOrigins {
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		return s.originSet[origin]
	}
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Host != "" {
			return s.originSet[u.Scheme+"://"+u.Host]
		}
	}
	return false
}

func (s *Server) sitePage(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	site := strings.TrimSpace(r.FormValue("site"))
	page := strings.TrimSpace(r.FormValue("page"))
	if site == "" || page == "" {
		http.Error(w, "site and page are required", http.StatusBadRequest)
		return "", "", false
	}
	if len(site) > 64 || len(page) > 400 {
		http.Error(w, "site or page too long (max 64/400)", http.StatusBadRequest)
		return "", "", false
	}
	return site, page, true
}
