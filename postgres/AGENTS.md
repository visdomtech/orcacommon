# PostgreSQL Utilities

Connection pool management, Atlas-based migration runner, Cloud SQL connector, embedded Postgres provisioning, and TestContainer support. Provides a single `OpenPool` entry point that handles the full lifecycle: connect → migrate → return pool.

## Architecture

```
OpenPool(ctx, dbcfg, migrator)
    │
    ├── CloudSQLInstance set → openCloudSQL() (cloudsqlconn dialer)
    │
    └── Connect(ctx, dbcfg.ResolveURL(), key)
         │
         ├── "postgres:embedded:" prefix
         │    └── Provision embedded Postgres (fergusstrange/embedded-postgres)
         │         ├── Reuse existing instance (check PID file + port liveness)
         │         └── Or start new instance on a free port
         │
         ├── "postgres:tc:" prefix
         │    └── Provision TestContainer (postgres Docker image)
         │
         └── Standard postgres:// URL → pgxpool.New()
    │
    └── runMigrations(ctx, pool, migrator)
         ├── Set search_path to MigrationSchema (default: "public")
         ├── Open Atlas postgres driver
         ├── Load migration files from embed.FS into Atlas MemDir
         │    └── Duplicate version prefix detection (clear error)
         ├── Acquire PostgreSQL advisory lock (key: 773492011, 30s timeout)
         ├── pgRevisions (custom RevisionReadWriter backed by atlas_schema_revisions)
         ├── Optional baseline (IsBaseline predicate → mark first migration as applied)
         │    └── Non-baseline path: WithAllowDirty(true)
         └── Execute pending migrations (non-linear order)
```

## Components

### `DBConfig` (`dbconfig.go`)
Database connection parameters with `env` struct tags (prefix `DB_`). `ResolveURL()` expands a URL template with credential placeholders. `IsEmbeddedPostgres()` and `IsTestContainer()` detect special connection modes. Implements `slog.LogValuer` (password redacted).

### `OpenPool` / `OpenPoolWithKey` (`pool.go`)
Process-wide singleton pool (or keyed pools for multi-database setups). Double-checked locking on a global map. `OpenPool` delegates to `OpenPoolWithKey` with the default key `"__shared__"` — consumers using `OpenPoolWithKey` should avoid this key to prevent collisions. Pool creation flow via `createPool`: connect → migrate → cache on success. On migration failure, the pool is closed and the error propagated — the pool is **not** cached (all-or-nothing). Uses pgxpool defaults (MaxConns = `runtime.NumCPU()`); consumers needing custom pool sizing should configure `pgxpool.Config` directly.

### `Connect` (`pool.go`)
Low-level connection function. Detects three special URL prefixes:
- **`postgres:embedded:`** — starts an embedded Postgres instance (pinned to `V18`). Query params: `?datapath=`, `?user=`, `?password=`, `?name=` (defaults: `test`/`test`/`test` when omitted). Reuses existing instances by checking PID file liveness + port. When reusing, `ensureDatabaseExists` creates the target database if absent (embedded-postgres only runs `createdb` during initial `initdb`).
- **`postgres:tc:`** — starts a TestContainer (default image: `postgres:17.5`). Image tag override is not currently functional due to a parsing bug in the tag extraction logic.
- **Standard URL** — connects directly via `pgxpool.New`.

### `Migrator` / `runMigrations` (`migrate.go`)
Atlas-based migration runner:
- **`Migrator`** — bundles `fs.FS` (migration files) with a caller-supplied `IsBaseline` predicate. Constructed via `NewMigrator(migrationFiles fs.FS, isBaseline func(ctx, pool) bool)`. A nil `isBaseline` disables baseline detection.
- **Advisory lock** — `pg_try_advisory_lock(773492011)` with 500ms polling, 30s max wait.
- **`pgRevisions`** — custom `RevisionReadWriter` backed by `atlas_schema_revisions` table (auto-created).
- **`embedDir`** — loads `fs.FS` into an Atlas `MemDir`, detects duplicate version prefixes.
- **Baseline** — when `IsBaseline` returns true, the first migration is recorded without executing SQL.

### Graceful Shutdown (`pool.go`)
`init()` launches `gracefulShutdown()` goroutine. On SIGTERM/SIGINT:
1. Close all keyed pools (drain connections).
2. Stop all embedded Postgres instances.

## Configuration

| Env Var (with `DB_` prefix) | Default | Description |
|---|---|---|
| `HOST` | `localhost` | PostgreSQL host |
| `PORT` | `5432` | PostgreSQL port |
| `USER` | — | PostgreSQL username |
| `PASSWORD` | — | PostgreSQL password |
| `NAME` | — | Database name |
| `CLOUD_SQL_INSTANCE` | — | Cloud SQL instance (`project:region:instance`) |
| `MIGRATION_SCHEMA` | `public` | PostgreSQL search_path for migrations |
| `URL_TEMPLATE` | `postgres:tc://[username]:[password]@[host]:[port]/[database_name]` | Connection URL template |

`ResolveURL()` also replaces `[query_parameters]` (with empty string) and URL-encodes the password via `url.QueryEscape`.

### Special URL prefixes
- `postgres:embedded:?datapath=/tmp/pgdata&user=dev&password=dev_only&name=mydb` (local dev only — never use weak credentials for real data)
- `postgres:tc:` (TestContainer, default `postgres:17.5`)
