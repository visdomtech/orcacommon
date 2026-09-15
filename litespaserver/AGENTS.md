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
Entry point. Routes requests to static-file or SPA-index paths. Applies security headers (`Cache-Control: no-store`, `X-Frame-Options`, `Referrer-Policy`, `X-Content-Type-Options`, CSP). Owns the version-keyed `indexCache` (capacity 10, arbitrary eviction — not LRU) and `singleflight.Group` for CDN fetches.

Public methods: `ServeRoot(w, r)`, `RefreshVersion(ctx)`, `Manager() *Manager`, `FlushCache()`. `FlushCache` fully clears the index cache and is auto-called via `Manager.OnChange` after a version update — consumers should expect a cold-cache fetch on the next request after a version change.

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
Serves an allow-list of static files. Paths support exact matches, single-segment globs (`/assets/*`), and recursive globs (`/assets/**`) via `doublestar`. Bounded in-memory cache (capacity 16, arbitrary eviction when at capacity), singleflight-guarded CDN fetches.

### CSP (`csp.go`)
Builds the `Content-Security-Policy` header from `CSPConfig` allow-lists. Falls back to built-in defaults. Per-request nonce is appended to `style-src`. Nonce generated from `crypto/rand` (alphanumeric, length 12).

`CSPConfig` also supports `Disable` (omit CSP header entirely) and `DisableAppendNonce` (omit per-request nonce from style-src).

> **SPA build contract:** The frontend build must emit `nonce="NONCE"` placeholders in inline style/script tags. The server replaces exactly the first occurrence per request with the generated nonce.

## Configuration

```go
litespaserver.Config{
    CDNPrefix:      "https://hc-cdn.example.com",  // CDN base URL
    CDNVersion:     "v1.2.3",                       // locks version, bypasses DB
    StaticPaths:     []string{"/unsubscribed.html", "/assets/**"},
    DefaultVersion:  "v1.0.0",                       // seeded into DB if absent
    CSP:             litespaserver.CSPConfig{...},   // optional CSP overrides
    EmbeddedContent: embeddedFS,                     // fs.FS for local dev (bypasses CDN + DB)
    FrontendName:    "admin",                        // namespaces version key for multi-SPA
    PublicAuth:      &ephemeralauth.Config{...},     // optional ephemeral token auth
}
```

### Version resolution priority
1. `EmbeddedContent` non-nil → version locked to `"embedded"`, DB never touched.
2. `CDNVersion` non-empty → version locked, DB never touched.
3. Otherwise → `dbProvider` reads from `litespa_settings` table, seeded with `DefaultVersion` if absent.

`FrontendName` namespaces the `litespa_settings` row key so multiple SPAs can share one database. Empty = `frontend.version`; non-empty = `frontend.version.<FrontendName>`.

## Prerequisites

Unless `EmbeddedContent` is used, consumers must create the `litespa_settings` table before calling `NewServer`. See `dao.go` for the required DDL.

## Ephemeral Auth Integration (`PublicAuth`)

When `Config.PublicAuth` is set to a non-nil `*ephemeralauth.Config`, the server:

1. **Applies guest session middleware** to `ServeRoot` — this is **cookie bootstrapping, not protection**. The `GuestSession` middleware never blocks requests; it only provisions a `guest_session` HMAC-signed cookie on the initial page load so the browser has it available for subsequent token issuance calls. The SPA page itself remains publicly accessible — the real gate is on the API endpoints (token issuance validates the cookie, and `Protect` middleware validates both the cookie and the JWT). The wrapped handler is cached in `wrappedServeRoot` at construction time to avoid per-request closure allocations.
2. **Exposes `PublicAuthHandler()`** — returns the `POST /api/auth/ephemeral-token` issuance handler (nil when not configured).
3. **Exposes `PublicAuthMiddleware()`** — returns stateless protection middleware enforcing signature, expiry, context binding (session/IP/UA), and `RequiredScopes` (nil when not configured).

Consumer wiring:
```go
server := litespaserver.NewServer(ctx, pool, cfg)

// Mount on consumer's router:
mux.Handle("/api/auth/ephemeral-token", server.PublicAuthHandler())
mux.Handle("/api/public/*", server.PublicAuthMiddleware()(apiHandler))
```

When `PublicAuth` is nil, all three methods return nil and `ServeRoot` behavior is unchanged — zero impact on existing consumers.

**Bot verifier:** When `PublicAuth.TurnstileSecret` is non-empty, a `TurnstileVerifier` is constructed automatically. If empty, `PublicAuthHandler()` returns nil (consumer must provide an external verifier via standalone `ephemeralauth` usage).

**Scope enforcement:** Set `PublicAuth.RequiredScopes` to enforce scope checks in `PublicAuthMiddleware()`. When empty, any valid token is accepted.

**Request lifecycle with PublicAuth:**
```
1. Browser → GET / (ServeRoot)
   GuestSession middleware: no valid cookie → generates session ID,
   signs it with HMAC, sets guest_session cookie via Set-Cookie.
   Page is served regardless. (Bootstrapping, not gating.)

2. SPA JS boots → renders Turnstile widget → solves challenge
   → obtains bot_token (client-side, from Cloudflare widget callback)

3. Browser → POST /api/auth/ephemeral-token { bot_token }
   Server validates: guest cookie (from step 1) + Turnstile token
   (via Cloudflare siteverify) → issues short-lived JWT bound to
   session ID, IP hash, and UA hash.

4. Browser → GET /api/public/data
   Authorization: Bearer <jwt>
   Protect middleware: verifies JWT signature + expiry, checks
   session/IP/UA binding match, checks scopes → passes or rejects.
```
