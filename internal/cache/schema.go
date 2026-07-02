package cache

// schemaSQL is idempotent: safe to run on every Open. Contentless FTS5 (content=”)
// means the index stores no column copy; Go syncs it explicitly (no triggers).
const schemaSQL = `
CREATE TABLE IF NOT EXISTS headers (
  rowid     INTEGER PRIMARY KEY,
  s3Key     TEXT NOT NULL UNIQUE,
  pk        TEXT NOT NULL,
  sk        TEXT NOT NULL,
  messageId TEXT,
  sender    TEXT,
  subject   TEXT,
  date      TEXT
);
CREATE INDEX IF NOT EXISTS idx_headers_mailbox ON headers(pk, sk DESC);
CREATE VIRTUAL TABLE IF NOT EXISTS headers_fts USING fts5(sender, subject, content='');
CREATE TABLE IF NOT EXISTS sync_state (
  mailbox   TEXT PRIMARY KEY,
  last_sk   TEXT,
  synced_at TEXT
);
`
