# Lite SPA Server

Serves a CDN-hosted single-page app: resolves the live frontend version, fetches `index.html` and an allow-list of static files from a CDN, injects a per-request CSP nonce, and serves the result. Generic and reusable — no dependency on any specific application's configuration.

## Architecture

```
Request → Server.ServeRoot()
    │
    ├── JSON Accept header → 404
    │
    ├── Static file (path matches allow-list glob)
    │    ├── Embedded FS mode → serve from fs.FS
    │    └── CDN mode → staticRetriever.retrieve() (singleflight, bounded cache)
    │
    └── SPA route (no file extension)
         │    (has extension but not static → 404 with base headers)
         ├── Generate per-request CSP nonce (crypto/rand)
         ├── Embedded FS mode → read index.html from fs.FS
         └── CDN mode
              ├── Check in-memory indexCache (version-keyed, capacity 10)
              └── fetcher.fetch() with singleflight collapse
                   └── CDN response validated: 2xx + body contains CDN prefix
```

## Components

### `Server` (`serve.go`)
Entry point. Routes requests to static-file or SPA-index paths. Applies security headers (`Cache-Control: no-store`, `X-Frame-Options`, `Referrer-Policy`, `X-Content-Type-Options`, CSP). Owns the version-keyed `indexCache` and `singleflight.Group` for CDN fetches.

### `Manager` (`version.go`)
Owns frontend version resolution via a `versionProvider` interface:
- **`staticProvider`** — fixed version from config (when `CDNVersion` is set or embedded mode).
- **`dbProvider`** — reads from `litespa_settings` table with a 5-minute TTL cache (singleflight-guarded). Falls back to cached value on DB error.

`SetVersion` validates the candidate against the CDN before persisting. `OnChange` listeners are fired after a successful version update (with panic recovery).

### `dao` (`dao.go`)
Reads/writes the `litespa_settings` key-value table. Required DDL:
```sql
CREATE TABLE "litespa_settings" (
  "id"         TEXT PRIMARY KEY,
  "value"      TEXT NOT NULL,
  "updated_on" TIMESTAMPTZ NOT NULL DEFAULT now()
);
```
The consuming service must create this table via their own migration.

### `fetcher` (`fetcher.go`)
Fetches `{cdn}/{version}/index.html` from the CDN. Validates: 2xx status AND response body contains the CDN prefix (guards against CDN error pages returning 200).

### `staticRetriever` (`static.go`)
Serves an allow-list of static files. Paths support exact matches, single-segment globs (`/assets/*`), and recursive globs (`/assets/**`) via `doublestar`. Bounded in-memory cache (capacity 16), singleflight-guarded CDN fetches.

### CSP (`csp.go`)
Builds the `Content-Security-Policy` header from `CSPConfig` allow-lists. Falls back to built-in defaults matching the doublefin SPA. Per-request nonce is appended to `style-src`. Nonce generated from `crypto/rand` (alphanumeric, length 12).

`CSPConfig` also supports `Disable` (omit CSP header entirely) and `DisableAppendNonce` (omit per-request nonce from style-src).

## Configuration

```go
litespaserver.Config{
    CDNPrefix:      "https://hc-cdn.example.com",  // CDN base URL
    CDNVersion:     "v1.2.3",                       // locks version, bypasses DB
    StaticPaths:     []string{"/unsubscribed.html", "/assets/**"},
    DefaultVersion:  "v1.0.0",                       // seeded into DB if absent
    CSP:             litespaserver.CSPConfig{...},   // optional CSP overrides
    EmbeddedContent: embeddedFS,                     // fs.FS for local dev (bypasses CDN + DB)
}
```

### Version resolution priority
1. `EmbeddedContent` non-nil → version locked to `"embedded"`, DB never touched.
2. `CDNVersion` non-empty → version locked, DB never touched.
3. Otherwise → `dbProvider` reads from `litespa_settings` table, seeded with `DefaultVersion` if absent.

## Prerequisites

Unless `EmbeddedContent` is used, consumers must create the `litespa_settings` table before calling `NewServer`. See `dao.go` for the required DDL.
