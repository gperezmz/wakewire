package sqlitestore

// schemaVersion is meta's schema_version once migrate has run.
const schemaVersion = 1

// migrations[i] takes the schema from version i to i+1.
var migrations = []string{`
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE watches (
  id INTEGER PRIMARY KEY,
  repo TEXT NOT NULL,
  identity TEXT NOT NULL,
  forge_account TEXT NOT NULL DEFAULT '',
  tracker TEXT NOT NULL DEFAULT '',
  tracker_identity TEXT NOT NULL DEFAULT '',
  tracker_account TEXT NOT NULL DEFAULT '',
  labels TEXT NOT NULL DEFAULT '{}',
  marker TEXT NOT NULL DEFAULT '',
  interval_ms INTEGER NOT NULL DEFAULT 0,
  paused_since TEXT,
  paused_until TEXT,
  polled_at TEXT,
  created_at TEXT NOT NULL,
  UNIQUE (repo, identity)
);
CREATE TABLE registrations (
  watch_id INTEGER PRIMARY KEY REFERENCES watches(id) ON DELETE CASCADE,
  harness TEXT NOT NULL,
  session TEXT NOT NULL,
  pid INTEGER NOT NULL,
  pid_start TEXT NOT NULL,
  registered_at TEXT NOT NULL,
  heard_at TEXT NOT NULL,
  ended_at TEXT,
  end_reason TEXT NOT NULL DEFAULT '',
  replaced TEXT NOT NULL DEFAULT ''
);
CREATE INDEX registrations_session ON registrations(session);
CREATE INDEX registrations_replaced ON registrations(replaced);
CREATE TABLE adapters (
  session TEXT PRIMARY KEY,
  pid INTEGER NOT NULL,
  pid_start TEXT NOT NULL,
  hello_at TEXT NOT NULL,
  heard_at TEXT NOT NULL,
  protocol INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX adapters_process ON adapters(pid, pid_start);
CREATE TABLE snapshots (
  watch_id INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
  source TEXT NOT NULL,
  ref TEXT NOT NULL,
  data TEXT NOT NULL,
  PRIMARY KEY (watch_id, source, ref)
);
CREATE TABLE changes (
  id INTEGER PRIMARY KEY,
  watch_id INTEGER NOT NULL,
  repo TEXT NOT NULL,
  source TEXT NOT NULL,
  item TEXT NOT NULL,
  kind TEXT NOT NULL,
  at TEXT NOT NULL,
  found_at TEXT NOT NULL,
  status TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  batch_id INTEGER NOT NULL DEFAULT 0,
  data TEXT NOT NULL
);
CREATE INDEX changes_watch_status ON changes(watch_id, status);
CREATE TABLE batches (
  id INTEGER PRIMARY KEY,
  watch_id INTEGER NOT NULL,
  offered_at TEXT,
  acked_at TEXT
);
CREATE INDEX batches_watch ON batches(watch_id, acked_at);
CREATE TABLE batch_changes (
  batch_id INTEGER NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  change_id INTEGER NOT NULL,
  PRIMARY KEY (batch_id, change_id)
);
CREATE TABLE problems (
  id INTEGER PRIMARY KEY,
  watch_id INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
  source TEXT NOT NULL,
  code TEXT NOT NULL,
  text TEXT NOT NULL,
  since TEXT NOT NULL,
  delivered_at TEXT,
  cleared_at TEXT
);
CREATE TABLE source_state (
  watch_id INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
  source TEXT NOT NULL,
  data BLOB,
  failures INTEGER NOT NULL DEFAULT 0,
  rate TEXT NOT NULL DEFAULT '{}',
  skip_until TEXT,
  ok_at TEXT,
  PRIMARY KEY (watch_id, source)
);
CREATE TABLE leases (
  watch_id INTEGER PRIMARY KEY,
  pid INTEGER NOT NULL,
  pid_start TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
`}
