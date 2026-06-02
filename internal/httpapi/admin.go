package httpapi

import (
	"net/http"
	"time"

	"viewer-counter/internal/store"
)

func (s *Server) handlePages(w http.ResponseWriter, r *http.Request) {
	site := r.FormValue("site")
	limit := parseInt(r.FormValue("limit"), 100, 1, 1000)
	offset := parseInt(r.FormValue("offset"), 0, 0, 1<<30)

	pages, err := s.store.ListPages(r.Context(), site, limit, offset)
	if err != nil {
		s.logger.Error("list pages failed", "err", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	// Overlay the (possibly ahead-of-DB) in-memory total.
	for i := range pages {
		if t := s.agg.Total(store.Key{Site: pages[i].Site, Page: pages[i].Page}); t > pages[i].Total {
			pages[i].Total = t
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
}

func (s *Server) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	site, page, ok := s.sitePage(w, r)
	if !ok {
		return
	}
	interval := "hour"
	if r.FormValue("interval") == "day" {
		interval = "day"
	}
	to := parseTimeParam(r.FormValue("to"), time.Now().UTC())
	from := parseTimeParam(r.FormValue("from"), to.Add(-7*24*time.Hour))

	points, err := s.store.Timeseries(r.Context(), site, page, from, to, interval)
	if err != nil {
		s.logger.Error("timeseries query failed", "err", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	if points == nil {
		points = []store.Point{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site": site, "page": page, "interval": interval,
		"from": from, "to": to, "points": points,
	})
}

func (s *Server) handleByIP(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Events.Record {
		http.Error(w, "event recording is disabled; by-ip query unavailable", http.StatusConflict)
		return
	}
	site, page, ok := s.sitePage(w, r)
	if !ok {
		return
	}
	to := parseTimeParam(r.FormValue("to"), time.Now().UTC())
	from := parseTimeParam(r.FormValue("from"), to.Add(-7*24*time.Hour))
	limit := parseInt(r.FormValue("limit"), 100, 1, 1000)

	rows, err := s.store.ByIP(r.Context(), site, page, from, to, limit)
	if err != nil {
		s.logger.Error("by-ip query failed", "err", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []store.IPCount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site": site, "page": page, "from": from, "to": to, "rows": rows,
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Events.Record {
		http.Error(w, "event recording is disabled; events query unavailable", http.StatusConflict)
		return
	}
	site, page, ok := s.sitePage(w, r)
	if !ok {
		return
	}
	to := parseTimeParam(r.FormValue("to"), time.Now().UTC())
	from := parseTimeParam(r.FormValue("from"), to.Add(-7*24*time.Hour))
	limit := parseInt(r.FormValue("limit"), 100, 1, 1000)
	offset := parseInt(r.FormValue("offset"), 0, 0, 1<<30)

	rows, err := s.store.Events(r.Context(), site, page, from, to, limit, offset)
	if err != nil {
		s.logger.Error("events query failed", "err", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []store.EventRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site": site, "page": page, "from": from, "to": to, "rows": rows,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 3*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unhealthy", "db": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
