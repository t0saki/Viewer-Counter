# Viewer-Counter

[![CI](https://github.com/t0saki/Viewer-Counter/actions/workflows/ci.yml/badge.svg)](https://github.com/t0saki/Viewer-Counter/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

A small, high-throughput page-view counter. A single endpoint records a view on
page load; public endpoints expose totals and recent counts; an authenticated
API exposes detailed analytics; a built-in dashboard visualizes it all.

Built for the **tens-to-hundreds QPS** range with deliberately low operational
complexity: Go + PostgreSQL, two dependencies, a single static binary.

## How it works

```
  page load ──► POST /api/v1/hit ──► [bot filter] ──► [dedup TTL] ──► in-memory counter
                                                                          │
   GET /api/v1/count  ◄── real-time total (memory)                       │ write-behind
   GET /api/v1/recent ◄── hourly buckets (Postgres)                         ▼ (every ~1s / batch)
   GET /api/v1/admin/* ◄── detailed queries (Postgres) ◄────────────── Postgres: counters
                                                                          + hourly buckets
                                                                          + raw events (optional)
```

- **Totals are real-time** — served from an in-memory atomic counter (seeded from
  the DB at startup), so reads never touch the database.
- **Writes are buffered** — increments accumulate in memory and a background
  goroutine flushes deltas to Postgres on an interval (default 1s) or when the
  buffer fills. This is what lets a single instance absorb bursts cheaply.
- **Accuracy is eventually-consistent** for `recent`/timeseries (≤1s lag, hourly
  granularity). On a DB hiccup, counter/bucket deltas are retried; in-memory
  totals stay correct.

## Quick start (Docker)

```bash
# Edit the secrets in docker-compose.yml first (VC_IP_SALT, VC_ADMIN_TOKEN)!
docker compose up --build -d
```

This starts PostgreSQL 16 and the app on `http://localhost:8080`. Open the dashboard
at `http://localhost:8080/dashboard`.

## Quick start (local)

```bash
cp config.example.yaml config.yaml   # then edit db.dsn, privacy.salt, auth.admin_tokens
make run                             # or: go run ./cmd/server -config config.yaml
```

You need a reachable PostgreSQL (14+). Tables and indexes are created
automatically on startup.

## Tracking snippet

Drop this on any page (uses `sendBeacon`, falls back to an image):

```html
<script>
(function () {
  var url = "http://localhost:8080/api/v1/hit?site=mysite&page=" +
            encodeURIComponent(location.pathname);
  if (navigator.sendBeacon) navigator.sendBeacon(url);
  else new Image().src = url;
})();
</script>
```

No-JS fallback (pixel):

```html
<img src="http://localhost:8080/pixel.gif?site=mysite&page=/home" alt="" width="1" height="1">
```

## API

### Public (no auth)

| Method     | Path                | Params                     | Returns |
|------------|---------------------|----------------------------|---------|
| GET/POST   | `/api/v1/hit`       | `site`, `page`             | `{ok, count?}` — records a view |
| GET        | `/pixel.gif`        | `site`, `page`             | 1×1 GIF, records a view |
| GET        | `/api/v1/count`     | `site`, `page`             | `{count}` — total (real-time) |
| GET        | `/api/v1/recent`    | `site`, `page`, `window?`  | `{window, count}` — views in window (default 24h, clamped to `recent.max`) |
| GET        | `/healthz`          | –                          | `{status}` (+ DB ping) |

`window` is a Go duration string (e.g. `1h`, `30m`, `7d`→use `168h`).

### Admin (bearer token)

Send `Authorization: Bearer <token>` (or `X-API-Key: <token>`). Tokens come from
`auth.admin_tokens` / `VC_ADMIN_TOKEN`.

| Path                        | Params                                          | Returns |
|-----------------------------|-------------------------------------------------|---------|
| `/api/v1/admin/pages`       | `site?`, `limit?`, `offset?`                    | per-page totals |
| `/api/v1/admin/timeseries`  | `site`, `page`, `from?`, `to?`, `interval?`     | bucketed counts (`hour`\|`day`) |
| `/api/v1/admin/by-ip`       | `site`, `page`, `from?`, `to?`, `limit?`        | counts grouped by IP hash* |
| `/api/v1/admin/events`      | `site`, `page`, `from?`, `to?`, `limit?`, `offset?` | raw events* |

`from`/`to` accept RFC3339 or unix seconds. *Requires `events.record: true`
(otherwise returns `409`).

## Configuration

See [`config.example.yaml`](./config.example.yaml) for the full annotated set.

**Every** setting can be provided via a YAML file, an environment variable, or
both — env vars override the file, so you can run with no config file at all.
Env vars are named `VC_<SECTION>_<FIELD>`. Slices are comma-separated; durations
use Go strings (`30m`, `24h`); bools accept `true`/`false`/`1`/`0`. An unset var
leaves the file/default value untouched.

| Env var | Maps to |
|---|---|
| `VC_SERVER_ADDR` | `server.addr` |
| `VC_SERVER_READ_TIMEOUT` / `_WRITE_TIMEOUT` / `_IDLE_TIMEOUT` | `server.*_timeout` |
| `VC_SERVER_TRUST_PROXY` | `server.trust_proxy` |
| `VC_SERVER_REAL_IP_HEADERS` | `server.real_ip_headers` (csv) |
| `VC_SERVER_MAX_BODY_BYTES` | `server.max_body_bytes` |
| `VC_DB_DSN` | `db.dsn` |
| `VC_DB_MAX_OPEN_CONNS` / `_MAX_IDLE_CONNS` | `db.*` |
| `VC_DB_CONN_MAX_LIFETIME` | `db.conn_max_lifetime` |
| `VC_PRIVACY_IP_MODE` | `privacy.ip_mode` |
| `VC_PRIVACY_RECORD_UA` | `privacy.record_ua` |
| `VC_PRIVACY_SALT` (or legacy `VC_IP_SALT`) | `privacy.salt` |
| `VC_DEDUP_ENABLED` / `VC_DEDUP_WINDOW` | `dedup.*` |
| `VC_RECENT_DEFAULT` / `VC_RECENT_MAX` | `recent.*` |
| `VC_CORS_ALLOWED_ORIGINS` (csv) / `VC_CORS_ENFORCE_ORIGIN` | `cors.*` |
| `VC_RATE_LIMIT_ENABLED` / `_RPS` / `_BURST` | `rate_limit.*` |
| `VC_FLUSH_INTERVAL` / `VC_FLUSH_BATCH` | `flush.*` |
| `VC_AUTH_ADMIN_TOKENS` (csv; or legacy `VC_ADMIN_TOKEN`) | `auth.admin_tokens` |
| `VC_BOT_ENABLED` / `VC_BOT_KEYWORDS` (csv) | `bot.*` |
| `VC_EVENTS_RECORD` | `events.record` |
| `VC_RETURN_COUNT` | `return_count` |

Key knobs:

- **`privacy.ip_mode`**: `none` | `hash` (salted SHA256, default, GDPR-friendly) |
  `truncate` (/24 v4, /48 v6) | `full`. `privacy.record_ua` toggles UA storage.
- **`dedup.window`** (default 30m): same IP+UA on the same page counts once.
- **`cors.allowed_origins`** + **`cors.enforce_origin`**: when enforcing (and not
  `*`), `/hit` rejects requests whose Origin/Referer isn't allowlisted.
- **`rate_limit`**: per-IP token bucket.
- **`events.record`**: disable for lighter writes / stronger privacy (totals and
  hourly buckets still work; by-IP/events queries become unavailable).

## Anti-abuse notes

CORS alone does **not** stop scripted abuse (`curl` ignores it). Protection comes
from the combination of: Origin/Referer allowlist enforcement, per-IP rate
limiting, IP+UA dedup, and bot-UA filtering.

## Behind a reverse proxy (getting the real client IP)

Without configuration the app sees the TCP peer, which behind a proxy is the
**proxy's** IP — every visitor collapses to one IP and dedup / rate-limit /
by-IP all break. To fix:

1. Set `server.trust_proxy: true`.
2. Set `server.real_ip_headers` to the header(s) your proxy sets to the real
   client IP — e.g. `["X-Real-IP"]` for nginx, `["CF-Connecting-IP"]` for
   Cloudflare, `["True-Client-IP"]` for some CDNs. The first non-empty one wins.

Two cautions:

- **Only trust these when the app is not directly reachable.** If a client can
  hit the app without going through the proxy, it can forge these headers. Keep
  the app on an internal network / bind it so only the proxy reaches it.
- The **left-most `X-Forwarded-For` value is never trusted** — it is
  client-supplied and spoofable. When falling back to XFF the app uses the
  right-most entry (the IP observed by the nearest proxy). Prefer an explicit
  `real_ip_headers` entry over relying on XFF.

## Development

```bash
make build   # build binary into bin/
make test    # unit + in-process HTTP integration tests (no DB needed)
make vet
make tidy
```

The test suite runs the full handler chain in-process with a fake flusher;
DB-backed behavior is exercised via `docker compose up`.

## Scaling beyond one instance

This is a single-instance design (in-memory counters + per-instance dedup/rate
limit). To run multiple replicas you'd move shared state to Redis (atomic INCR,
dedup set, rate limiter) and keep Postgres for persistence — out of scope here given
the target load.
