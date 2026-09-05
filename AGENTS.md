# OrcaCommon — Shared Go Library

Shared Go library for Visdom/Orca services. Provides reusable infrastructure: PostgreSQL connection pooling and migrations (Atlas-based, with Cloud SQL and embedded Postgres support), CDN-hosted SPA serving with CSP nonce injection, transactional email via Mailgun, and general-purpose utilities.

**Module**: `github.com/visdomtech/orcacommon`
**Go version**: 1.26.4

---

## Global System Architecture

```
orcacommon/
├── email/              Mailgun transactional email sender
├── litespaserver/      CDN-hosted SPA server with CSP nonce injection
├── postgres/           PostgreSQL pool, migrations (Atlas), Cloud SQL, embedded PG
└── utils/              Struct↔map conversion, network helpers, embedded PG probes, slog handler
```

All packages are stateless libraries consumed by downstream services. The only process-level side effect is `postgres.init()`, which registers a SIGTERM/SIGINT handler for graceful pool and embedded-PG shutdown.

> **Internal dependency:** `postgres/` imports `utils/` (for embedded-PG probes and `GetFreePort`). All other packages are independent.

---

## Sub-Module Architecture & Directory Guides

### 1. [Email Package](email/AGENTS.md)
* **Responsibility**: Mailgun-backed transactional email sender. `Sender` interface for mock injection, `MailgunClient` with connection pooling, file attachments (≤ 10 MB), CC/BCC support.
* **Path**: `email/`

### 2. [Lite SPA Server](litespaserver/AGENTS.md)
* **Responsibility**: Serves a CDN-hosted single-page app — resolves the live frontend version (locked via env, DB-backed, or embedded FS), proxies `index.html` + an allow-list of static files from the CDN, and injects a per-request CSP nonce.
* **Path**: `litespaserver/`

### 3. [PostgreSQL Utilities](postgres/AGENTS.md)
* **Responsibility**: Connection pool management (`pgxpool`), Atlas-based migration runner with advisory locking and baseline support, Cloud SQL connector, embedded Postgres provisioning (`fergusstrange/embedded-postgres`), and TestContainer support.
* **Path**: `postgres/`

### 4. [Utility Functions](utils/AGENTS.md)
* **Responsibility**: Struct↔map conversion (JSON and reflection-based), network helpers (localhost detection, free port allocation, JSON response writer), embedded Postgres process probes (PID file, port liveness), and a split-level `slog.Handler`.
* **Path**: `utils/`

---

## Core Development & Operation Rules

### 1. Build and Test
* The entire module must compile cleanly (`go build ./...`) before committing.
* Run tests with the race detector: `go test -race ./...`.
* **Unit tests:** pure Go, no external deps — `go test -race ./...`
* **Integration tests:** `postgres/` uses `//go:build integration` tag — run with `go test -race -tags=integration ./postgres/...` (requires Docker for TestContainers)
* **Platform-guarded tests:** `utils/embedded_pg_test.go` is excluded on Windows via `//go:build !windows`

### 2. Dependency Discipline
* Keep the dependency tree lean. New external dependencies must be justified.
* Prefer `pgx/v5` over `lib/pq` for all PostgreSQL operations.
* The `email` package has zero external dependencies beyond the Go standard library.

### 3. Configuration Convention
* Struct tags follow the [caarlos0/env](https://github.com/caarlos0/env) convention (`env:"FIELD_NAME"`).
* Consuming services are responsible for parsing env vars; this library does not auto-read them.
* All config structs implement `slog.LogValuer` to redact secrets (passwords, API keys).

### 4. Graceful Shutdown
* `postgres.init()` registers a process-wide SIGTERM/SIGINT handler that closes all connection pools and stops embedded Postgres instances.
* Do not register additional signal handlers that conflict with this.

### 5. Migration Safety
* Atlas migrations use a PostgreSQL advisory lock (`773492011`) to serialise across replicas.
* Migration files are embedded via `//go:embed` and loaded into an Atlas `MemDir`.
* Duplicate migration version prefixes are detected early with a clear error.
