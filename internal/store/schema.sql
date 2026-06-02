CREATE TABLE IF NOT EXISTS page_counters (
  site_key    VARCHAR(64)  NOT NULL,
  page_key    VARCHAR(400) NOT NULL,
  total_count BIGINT       NOT NULL DEFAULT 0,
  updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
  PRIMARY KEY (site_key, page_key)
);

CREATE TABLE IF NOT EXISTS view_buckets (
  site_key     VARCHAR(64)  NOT NULL,
  page_key     VARCHAR(400) NOT NULL,
  bucket_start TIMESTAMPTZ  NOT NULL,
  cnt          BIGINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (site_key, page_key, bucket_start)
);

CREATE TABLE IF NOT EXISTS view_events (
  id       BIGSERIAL    PRIMARY KEY,
  site_key VARCHAR(64)  NOT NULL,
  page_key VARCHAR(400) NOT NULL,
  ts       TIMESTAMPTZ  NOT NULL,
  ip_hash  VARCHAR(64),
  ua       VARCHAR(512),
  referer  VARCHAR(512)
);

CREATE INDEX IF NOT EXISTS idx_site_page_ts ON view_events (site_key, page_key, ts);

CREATE INDEX IF NOT EXISTS idx_site_ip ON view_events (site_key, ip_hash)
