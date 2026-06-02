CREATE TABLE IF NOT EXISTS page_counters (
  site_key    VARCHAR(64)  NOT NULL,
  page_key    VARCHAR(400) NOT NULL,
  total_count BIGINT       NOT NULL DEFAULT 0,
  updated_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (site_key, page_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS view_buckets (
  site_key     VARCHAR(64)  NOT NULL,
  page_key     VARCHAR(400) NOT NULL,
  bucket_start DATETIME     NOT NULL,
  cnt          BIGINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (site_key, page_key, bucket_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS view_events (
  id       BIGINT       NOT NULL AUTO_INCREMENT,
  site_key VARCHAR(64)  NOT NULL,
  page_key VARCHAR(400) NOT NULL,
  ts       DATETIME     NOT NULL,
  ip_hash  VARCHAR(64)  NULL,
  ua       VARCHAR(512) NULL,
  referer  VARCHAR(512) NULL,
  PRIMARY KEY (id),
  KEY idx_site_page_ts (site_key, page_key, ts),
  KEY idx_site_ip (site_key, ip_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 ROW_FORMAT=DYNAMIC
