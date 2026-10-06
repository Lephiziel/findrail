CREATE TABLE github_sources (
    source_id TEXT PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    repository_id INTEGER NOT NULL,
    owner TEXT NOT NULL,
    repo TEXT NOT NULL,
    repository_url TEXT NOT NULL,
    ref_mode TEXT NOT NULL,
    ref_value TEXT NOT NULL,
    selected_path TEXT NOT NULL,
    max_bytes INTEGER NOT NULL,
    policy_version INTEGER NOT NULL,
    snapshot_sha TEXT NOT NULL,
    commit_time TEXT NOT NULL,
    registration_token TEXT NOT NULL,
    revision INTEGER NOT NULL,
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX github_sources_repository ON github_sources(repository_id);
PRAGMA user_version = 3;
