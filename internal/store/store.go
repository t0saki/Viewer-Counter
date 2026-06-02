// Package store wraps the PostgreSQL persistence layer: schema migration,
// counter loading, write-behind flushes, and analytical queries.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"viewer-counter/internal/config"
)

//go:embed schema.sql
var schemaSQL string

// Key identifies a counter (site + page).
type Key struct {
	Site string
	Page string
}

// BucketKey identifies an hourly aggregation bucket.
type BucketKey struct {
	Site string
	Page string
	Hour time.Time
}

// Event is a single recorded page view.
type Event struct {
	Site    string
	Page    string
	TS      time.Time
	IPHash  string
	UA      string
	Referer string
}

// Point is one entry in a time series.
type Point struct {
	Bucket time.Time `json:"bucket"`
	Count  int64     `json:"count"`
}

// IPCount is a per-IP aggregation row.
type IPCount struct {
	IPHash string `json:"ip_hash"`
	Count  int64  `json:"count"`
}

// PageRow is a per-page total row.
type PageRow struct {
	Site      string    `json:"site"`
	Page      string    `json:"page"`
	Total     int64     `json:"total"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EventRow is a single event for detailed listing.
type EventRow struct {
	TS      time.Time `json:"ts"`
	IPHash  string    `json:"ip_hash,omitempty"`
	UA      string    `json:"ua,omitempty"`
	Referer string    `json:"referer,omitempty"`
}

type Store struct {
	db *sql.DB
}

const insertChunk = 500

func Open(cfg config.DBConfig) (*Store, error) {
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime.Std())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Migrate creates tables/indexes if they do not exist.
func (s *Store) Migrate(ctx context.Context) error {
	for _, stmt := range strings.Split(schemaSQL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w\nstatement: %s", err, stmt)
		}
	}
	return nil
}

// LoadCounters returns all persisted totals, used to seed the in-memory map.
func (s *Store) LoadCounters(ctx context.Context) (map[Key]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT site_key, page_key, total_count FROM page_counters")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[Key]int64)
	for rows.Next() {
		var k Key
		var c int64
		if err := rows.Scan(&k.Site, &k.Page, &c); err != nil {
			return nil, err
		}
		out[k] = c
	}
	return out, rows.Err()
}

// valuesClause builds a positional VALUES list like "($1,$2),($3,$4)" for the
// given row/column counts.
func valuesClause(rows, cols int) string {
	var b strings.Builder
	idx := 1
	for r := 0; r < rows; r++ {
		if r > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		for c := 0; c < cols; c++ {
			if c > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(idx))
			idx++
		}
		b.WriteByte(')')
	}
	return b.String()
}

// FlushCounters applies accumulated total deltas via upsert.
func (s *Store) FlushCounters(ctx context.Context, deltas map[Key]int64) error {
	if len(deltas) == 0 {
		return nil
	}
	type kv struct {
		k Key
		v int64
	}
	batch := make([]kv, 0, len(deltas))
	for k, v := range deltas {
		batch = append(batch, kv{k, v})
	}
	for i := 0; i < len(batch); i += insertChunk {
		part := batch[i:min(i+insertChunk, len(batch))]
		args := make([]any, 0, len(part)*3)
		for _, item := range part {
			args = append(args, item.k.Site, item.k.Page, item.v)
		}
		q := "INSERT INTO page_counters (site_key, page_key, total_count) VALUES " +
			valuesClause(len(part), 3) +
			" ON CONFLICT (site_key, page_key) DO UPDATE SET " +
			"total_count = page_counters.total_count + EXCLUDED.total_count, updated_at = now()"
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

// FlushBuckets applies accumulated hourly-bucket deltas via upsert.
func (s *Store) FlushBuckets(ctx context.Context, deltas map[BucketKey]int64) error {
	if len(deltas) == 0 {
		return nil
	}
	type kv struct {
		k BucketKey
		v int64
	}
	batch := make([]kv, 0, len(deltas))
	for k, v := range deltas {
		batch = append(batch, kv{k, v})
	}
	for i := 0; i < len(batch); i += insertChunk {
		part := batch[i:min(i+insertChunk, len(batch))]
		args := make([]any, 0, len(part)*4)
		for _, item := range part {
			args = append(args, item.k.Site, item.k.Page, item.k.Hour, item.v)
		}
		q := "INSERT INTO view_buckets (site_key, page_key, bucket_start, cnt) VALUES " +
			valuesClause(len(part), 4) +
			" ON CONFLICT (site_key, page_key, bucket_start) DO UPDATE SET " +
			"cnt = view_buckets.cnt + EXCLUDED.cnt"
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

// InsertEvents batch-inserts raw view events.
func (s *Store) InsertEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	for i := 0; i < len(events); i += insertChunk {
		part := events[i:min(i+insertChunk, len(events))]
		args := make([]any, 0, len(part)*6)
		for _, ev := range part {
			args = append(args, ev.Site, ev.Page, ev.TS, nullStr(ev.IPHash), nullStr(ev.UA), nullStr(ev.Referer))
		}
		q := "INSERT INTO view_events (site_key, page_key, ts, ip_hash, ua, referer) VALUES " +
			valuesClause(len(part), 6)
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

// Recent returns the view count since the given time using hourly buckets.
func (s *Store) Recent(ctx context.Context, site, page string, since time.Time) (int64, error) {
	var c int64
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(cnt),0)::bigint FROM view_buckets "+
			"WHERE site_key=$1 AND page_key=$2 AND bucket_start >= $3",
		site, page, since).Scan(&c)
	if err != nil {
		return 0, err
	}
	return c, nil
}

// Timeseries returns bucketed counts between [from, to) at hour or day
// granularity.
func (s *Store) Timeseries(ctx context.Context, site, page string, from, to time.Time, interval string) ([]Point, error) {
	// Hourly buckets are already stored per hour, so the hour case needs no
	// aggregation. The day case rolls hours up in UTC.
	var query string
	if interval == "day" {
		query = "SELECT date_trunc('day', bucket_start AT TIME ZONE 'UTC') AS b, SUM(cnt)::bigint " +
			"FROM view_buckets WHERE site_key=$1 AND page_key=$2 AND bucket_start >= $3 AND bucket_start < $4 " +
			"GROUP BY b ORDER BY b"
	} else {
		query = "SELECT bucket_start, cnt FROM view_buckets " +
			"WHERE site_key=$1 AND page_key=$2 AND bucket_start >= $3 AND bucket_start < $4 " +
			"ORDER BY bucket_start"
	}
	rows, err := s.db.QueryContext(ctx, query, site, page, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var points []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Bucket, &p.Count); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// ByIP returns per-IP-hash counts between [from, to).
func (s *Store) ByIP(ctx context.Context, site, page string, from, to time.Time, limit int) ([]IPCount, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT ip_hash, COUNT(*)::bigint FROM view_events "+
			"WHERE site_key=$1 AND page_key=$2 AND ts >= $3 AND ts < $4 AND ip_hash IS NOT NULL "+
			"GROUP BY ip_hash ORDER BY 2 DESC LIMIT $5",
		site, page, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IPCount
	for rows.Next() {
		var r IPCount
		if err := rows.Scan(&r.IPHash, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Events returns raw events between [from, to), newest first.
func (s *Store) Events(ctx context.Context, site, page string, from, to time.Time, limit, offset int) ([]EventRow, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT ts, ip_hash, ua, referer FROM view_events "+
			"WHERE site_key=$1 AND page_key=$2 AND ts >= $3 AND ts < $4 "+
			"ORDER BY ts DESC LIMIT $5 OFFSET $6",
		site, page, from, to, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var r EventRow
		var ip, ua, ref sql.NullString
		if err := rows.Scan(&r.TS, &ip, &ua, &ref); err != nil {
			return nil, err
		}
		r.IPHash, r.UA, r.Referer = ip.String, ua.String, ref.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPages returns per-page totals, optionally filtered by site, ordered by
// total descending.
func (s *Store) ListPages(ctx context.Context, site string, limit, offset int) ([]PageRow, error) {
	var (
		query string
		args  []any
	)
	if site != "" {
		query = "SELECT site_key, page_key, total_count, updated_at FROM page_counters " +
			"WHERE site_key=$1 ORDER BY total_count DESC LIMIT $2 OFFSET $3"
		args = []any{site, limit, offset}
	} else {
		query = "SELECT site_key, page_key, total_count, updated_at FROM page_counters " +
			"ORDER BY total_count DESC LIMIT $1 OFFSET $2"
		args = []any{limit, offset}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageRow
	for rows.Next() {
		var r PageRow
		if err := rows.Scan(&r.Site, &r.Page, &r.Total, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
