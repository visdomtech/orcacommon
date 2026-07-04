# Static File Serving

<cite>
**Referenced Files in This Document**
- [litespaserver.go](file://litespaserver/litespaserver.go)
- [serve.go](file://litespaserver/serve.go)
- [static.go](file://litespaserver/static.go)
- [static_test.go](file://litespaserver/static_test.go)
- [serve_test.go](file://litespaserver/serve_test.go)
- [fetcher.go](file://litespaserver/fetcher.go)
- [version.go](file://litespaserver/version.go)
- [csp.go](file://litespaserver/csp.go)
- [dao.go](file://litespaserver/dao.go)
- [index.html](file://litespaserver/testdata/embed/index.html)
- [unsubscribed.html](file://litespaserver/testdata/embed/unsubscribed.html)
</cite>

## Update Summary
**Changes Made**
- Updated StaticPaths allow-list mechanism to support glob pattern matching using doublestar library
- Enhanced static file serving logic to handle both exact matches and flexible pattern-based matching
- Added comprehensive documentation for single-segment '*' and recursive '**' glob patterns
- Updated testing examples to demonstrate glob pattern functionality
- Enhanced configuration examples to show pattern-based static file serving

## Table of Contents
1. [Introduction](#introduction)
2. [Project Structure](#project-structure)
3. [Core Components](#core-components)
4. [Architecture Overview](#architecture-overview)
5. [Detailed Component Analysis](#detailed-component-analysis)
6. [Dependency Analysis](#dependency-analysis)
7. [Performance Considerations](#performance-considerations)
8. [Troubleshooting Guide](#troubleshooting-guide)
9. [Conclusion](#conclusion)
10. [Appendices](#appendices)

## Introduction
This document explains the static file serving capabilities of the Lite SPA Server. It covers the enhanced StaticPaths allow-list mechanism with glob pattern support, file serving logic, caching strategies, integration with CDN proxy serving, file path resolution, and static file validation. The system now supports flexible pattern-based matching using the doublestar library, enabling both single-segment '*' and recursive '**' patterns for advanced static file serving scenarios. It also documents the differences between embedded content serving and CDN-based serving, performance considerations, cache management, and practical guidance for configuration and debugging.

## Project Structure
The static file serving logic is centered around a small set of cohesive packages:
- Configuration and public API surface
- Request routing and response handling
- Static file retrieval and caching with glob pattern support
- Index.html fetching and validation
- Version management and provider selection
- CSP generation and nonce injection
- DAO for persistent version storage

```mermaid
graph TB
subgraph "Public API"
LSS["litespaserver.go<br/>Config, CSPConfig"]
end
subgraph "HTTP Handler"
S["serve.go<br/>Server, ServeRoot, setBaseHeaders"]
end
subgraph "Static Retrieval"
SR["static.go<br/>staticRetriever, isStatic, retrieve, put<br/>Glob Pattern Support"]
end
subgraph "Index Fetching"
F["fetcher.go<br/>fetcher, fetch"]
end
subgraph "Version Management"
M["version.go<br/>Manager, providers"]
D["dao.go<br/>dao, getVersion, setVersion"]
end
subgraph "Security"
CSP["csp.go<br/>cspRule, generateNonce"]
end
LSS --> S
S --> SR
S --> F
S --> M
M --> D
S --> CSP
```

**Diagram sources**
- [litespaserver.go:10-57](file://litespaserver/litespaserver.go#L10-L57)
- [serve.go:29-228](file://litespaserver/serve.go#L29-L228)
- [static.go:17-120](file://litespaserver/static.go#L17-L120)
- [fetcher.go:12-70](file://litespaserver/fetcher.go#L12-L70)
- [version.go:18-199](file://litespaserver/version.go#L18-L199)
- [dao.go:15-56](file://litespaserver/dao.go#L15-L56)
- [csp.go:62-115](file://litespaserver/csp.go#L62-L115)

**Section sources**
- [litespaserver.go:10-57](file://litespaserver/litespaserver.go#L10-L57)
- [serve.go:29-228](file://litespaserver/serve.go#L29-L228)
- [static.go:17-120](file://litespaserver/static.go#L17-L120)
- [fetcher.go:12-70](file://litespaserver/fetcher.go#L12-L70)
- [version.go:18-199](file://litespaserver/version.go#L18-L199)
- [dao.go:15-56](file://litespaserver/dao.go#L15-L56)
- [csp.go:62-115](file://litespaserver/csp.go#L62-L115)

## Core Components
- StaticPaths allow-list with glob pattern support: A list of static file paths that are permitted to be served via CDN proxy, supporting exact matches, single-segment globs ('*' pattern), and recursive globs ('**' pattern). Only paths matching entries in this list are eligible for static serving.
- staticRetriever: Manages the allow-list with glob pattern matching, caches successful CDN responses, and deduplicates concurrent fetches for the same URL.
- Server.ServeRoot: Orchestrates request handling: rejects JSON requests, proxies static files from CDN or embedded FS using glob pattern matching, and serves index.html with per-request CSP nonce.
- fetcher: Validates that index.html originates from the configured CDN by checking the response body for the CDN prefix.
- Manager and providers: Resolve the live frontend version from either a static provider (development), a database-backed provider (production), or embedded content mode.
- CSP and nonce: Generates CSP headers with a per-request nonce and injects it into index.html.

**Section sources**
- [litespaserver.go:21-23](file://litespaserver/litespaserver.go#L21-L23)
- [static.go:27-49](file://litespaserver/static.go#L27-L49)
- [serve.go:93-188](file://litespaserver/serve.go#L93-L188)
- [fetcher.go:32-69](file://litespaserver/fetcher.go#L32-L69)
- [version.go:91-120](file://litespaserver/version.go#L91-L120)
- [csp.go:62-115](file://litespaserver/csp.go#L62-L115)

## Architecture Overview
The static file serving pipeline integrates CDN proxying, embedded FS serving, and caching with enhanced glob pattern support. Requests are routed based on path extension and glob pattern membership. Static files are served directly from the embedded filesystem when available; otherwise they are fetched from the CDN and cached. Index.html is fetched and validated against the CDN prefix, then cached per version.

```mermaid
sequenceDiagram
participant Client as "Client"
participant Server as "Server.ServeRoot"
participant Static as "staticRetriever"
participant CDN as "CDN"
participant FS as "Embedded FS"
participant Fetcher as "fetcher"
participant Manager as "Manager"
Client->>Server : GET /
Server->>Manager : Version(ctx)
Manager-->>Server : version
Server->>Server : Determine route (JSON? static? index?)
alt JSON request
Server-->>Client : 404 Not Found
else Static file with glob pattern
alt Embedded mode enabled
Server->>FS : ReadFile(path)
FS-->>Server : bytes or error
alt Found
Server-->>Client : 200 with base headers
else Not found
Server->>Static : isStatic(glob pattern match)
Static->>Static : Match against patterns (* or **)
Static-->>Server : true/false
alt Pattern match
Server->>Static : retrieve(cdn, version, path)
Static->>CDN : GET {cdn}/{version}{path}
CDN-->>Static : body
Static-->>Server : body
Server-->>Client : 200 with base headers
else No match
Server-->>Client : 404 Not Found
end
else CDN mode
Server->>Static : isStatic(glob pattern match)
Static->>Static : Match against patterns (* or **)
Static-->>Server : true/false
alt Pattern match
Server->>Static : retrieve(cdn, version, path)
Static->>CDN : GET {cdn}/{version}{path}
CDN-->>Static : body
Static-->>Server : body
Server-->>Client : 200 with base headers
else No match
Server-->>Client : 404 Not Found
end
end
else Index.html
Server->>Server : Generate nonce
alt Embedded mode enabled
Server->>FS : ReadFile(index.html)
FS-->>Server : bytes or error
Server-->>Client : 200 with CSP nonce injected
else CDN mode
Server->>Server : Lookup indexCache[version]
alt Cache hit
Server-->>Client : 200 with CSP nonce injected
else Cache miss
Server->>Fetcher : fetch(cdn, version)
Fetcher->>CDN : GET {cdn}/{version}/index.html
CDN-->>Fetcher : body
Fetcher-->>Server : {url, content}
Server->>Server : Store in indexCache
Server-->>Client : 200 with CSP nonce injected
end
end
end
```

**Diagram sources**
- [serve.go:93-188](file://litespaserver/serve.go#L93-L188)
- [static.go:51-61](file://litespaserver/static.go#L51-L61)
- [static.go:52-95](file://litespaserver/static.go#L52-L95)
- [fetcher.go:32-69](file://litespaserver/fetcher.go#L32-L69)
- [version.go:138-146](file://litespaserver/version.go#L138-L146)

## Detailed Component Analysis

### Enhanced StaticPaths Allow-list Mechanism with Glob Patterns
- Purpose: Restrict which static files can be served via CDN proxy using flexible glob pattern matching. Supports exact matches, single-segment globs ('*' pattern), and recursive globs ('**' pattern).
- Construction: The allow-list is built from the Config.StaticPaths slice during Server initialization, with each path stored as-is for pattern matching.
- Pattern Matching: During request handling, the path is checked against all patterns using the doublestar library. Patterns support:
  - Exact matches: "/unsubscribed.html"
  - Single-segment globs: "/assets/*" (matches one level deep)
  - Recursive globs: "/assets/**" (matches any depth)
- Validation: During request handling, the path extension and glob pattern membership determine whether a static file is served.

Key behaviors:
- Paths must be absolute and normalized (leading slash).
- Non-empty extensions are used to detect static file candidates.
- Unknown or disallowed static paths return 404.
- Malformed patterns (e.g., unterminated brackets) silently fail to match.

**Updated** Enhanced from exact-match-only functionality to support flexible pattern-based matching using doublestar library.

**Section sources**
- [litespaserver.go:21-23](file://litespaserver/litespaserver.go#L21-L23)
- [static.go:27-49](file://litespaserver/static.go#L27-L49)
- [static.go:51-61](file://litespaserver/static.go#L51-L61)
- [serve.go:109-136](file://litespaserver/serve.go#L109-L136)
- [static_test.go:11-22](file://litespaserver/static_test.go#L11-L22)
- [static_test.go:24-47](file://litespaserver/static_test.go#L24-L47)
- [static_test.go:49-70](file://litespaserver/static_test.go#L49-L70)

### Static File Serving Logic with Glob Pattern Support
- Embedded mode: When EmbeddedContent is configured and valid, static files are served directly from the filesystem. The path is derived by trimming the leading slash from the request path.
- CDN mode: When embedded mode is not active or the file is not present in the embedded FS, the staticRetriever checks if the request path matches any glob pattern in the allow-list before fetching from the CDN.
- Security headers: Static responses apply base security headers but do not include a CSP nonce.

Enhanced Flow:
- Detect JSON requests and return 404.
- If path has an extension and matches any glob pattern in the allow-list, serve static file.
- Otherwise, serve index.html with a fresh CSP nonce.

**Updated** Enhanced to use glob pattern matching instead of exact-only matching for static file eligibility determination.

**Section sources**
- [serve.go:93-188](file://litespaserver/serve.go#L93-L188)
- [serve.go:109-131](file://litespaserver/serve.go#L109-L131)
- [serve.go:190-202](file://litespaserver/serve.go#L190-L202)

### Caching Strategies
- Static file cache: In-memory cache keyed by the full CDN URL. Capacity is bounded; eviction removes an arbitrary entry when capacity is reached. This prevents unbounded memory growth while reducing repeated CDN fetches.
- Index.html cache: Per-version cache of the rendered index.html content. Capacity is bounded; eviction removes an arbitrary entry when capacity is reached.
- Single-flight: Both static retrieval and index.html fetching use singleflight to collapse concurrent requests for the same URL or version into a single upstream call.

```mermaid
flowchart TD
Start(["Static Retrieve"]) --> BuildURL["Build URL: {cdn}/{version}{path}"]
BuildURL --> CheckCache["Check in-memory cache"]
CheckCache --> Hit{"Cache hit?"}
Hit --> |Yes| ReturnCache["Return cached body"]
Hit --> |No| CheckPattern["Check glob patterns"]
CheckPattern --> PatternMatch{"Pattern match?"}
PatternMatch --> |No| Return404["Return 404 Not Found"]
PatternMatch --> |Yes| SingleFlight["singleflight.Do(url, fetch)"]
SingleFlight --> FetchCDN["HTTP GET {cdn}/{version}{path}"]
FetchCDN --> StatusOK{"Status 2xx?"}
StatusOK --> |No| ReturnErr["Return error"]
StatusOK --> |Yes| PutCache["Put into cache"]
PutCache --> ReturnBody["Return body"]
ReturnCache --> End(["Done"])
ReturnBody --> End
ReturnErr --> End
Return404 --> End
```

**Diagram sources**
- [static.go:52-95](file://litespaserver/static.go#L52-L95)
- [static.go:97-108](file://litespaserver/static.go#L97-L108)
- [static.go:51-61](file://litespaserver/static.go#L51-L61)

**Section sources**
- [static.go:14-15](file://litespaserver/static.go#L14-L15)
- [static.go:97-108](file://litespaserver/static.go#L97-L108)
- [serve.go:204-221](file://litespaserver/serve.go#L204-L221)
- [serve.go:167-176](file://litespaserver/serve.go#L167-L176)

### CDN Proxy Serving and File Path Resolution with Glob Patterns
- URL construction: Static files are requested as {cdn}/{version}{path}. The version comes from the Manager.
- Path resolution: The request path is checked against glob patterns in the allow-list. The pattern matching uses the doublestar library for flexible matching.
- Embedded fallback: If the embedded FS is configured and contains the requested static file, it is served directly without hitting the CDN.
- Pattern matching behavior:
  - Single-segment '*' does not cross directory boundaries
  - Recursive '**' crosses directory boundaries for nested matching
  - Exact matches work as before

Validation:
- Static retrieval returns an error on non-2xx responses.
- Index.html retrieval validates that the response body contains the configured CDN prefix.

**Updated** Enhanced to support glob pattern matching for path resolution and validation.

**Section sources**
- [static.go:55-56](file://litespaserver/static.go#L55-L56)
- [static.go:51-61](file://litespaserver/static.go#L51-L61)
- [serve.go:111-121](file://litespaserver/serve.go#L111-L121)
- [fetcher.go:36-69](file://litespaserver/fetcher.go#L36-L69)

### Static File Validation with Pattern Matching
- Static retrieval: Returns an error when the upstream response is non-2xx.
- Index.html validation: Ensures the response body contains the configured CDN prefix to guard against serving non-SPA error pages that still return 200.
- Pattern validation: Malformed glob patterns (e.g., unterminated brackets) silently fail to match, preventing configuration errors from causing unexpected behavior.

**Updated** Enhanced pattern validation to handle malformed patterns gracefully.

**Section sources**
- [static.go:83-86](file://litespaserver/static.go#L83-L86)
- [static.go:52-53](file://litespaserver/static.go#L52-L53)
- [fetcher.go:63-66](file://litespaserver/fetcher.go#L63-L66)

### Embedded Content Serving vs CDN-Based Serving with Glob Patterns
- Embedded mode:
  - Uses fs.FS to serve index.html and static files directly.
  - No CDN calls; avoids network latency and external dependencies.
  - Requires index.html at the root of the provided filesystem.
  - Supports glob patterns for determining which files to serve from embedded FS.
- CDN mode:
  - Proxies static files from the configured CDN.
  - Applies glob pattern filtering and caching.
  - Validates index.html against the CDN prefix.

Fallback behavior:
- If embedded mode is enabled but a static file is not present in the embedded FS, the server checks if the path matches any glob patterns before falling back to CDN retrieval for that file.

**Updated** Enhanced to support glob pattern matching in embedded mode for determining file eligibility.

**Section sources**
- [serve.go:110-131](file://litespaserver/serve.go#L110-L131)
- [serve.go:32-38](file://litespaserver/serve.go#L32-L38)
- [serve.go:61-75](file://litespaserver/serve.go#L61-L75)
- [serve_test.go:287-311](file://litespaserver/serve_test.go#L287-L311)

### CSP and Nonce Injection
- CSP headers: Applied to index.html responses with base security headers and optional per-request nonce appended to style-src.
- Nonce generation: Cryptographically secure random alphanumeric string.
- Nonce injection: Replaces the placeholder nonce="NONCE" in index.html with the generated nonce.

**Section sources**
- [serve.go:190-202](file://litespaserver/serve.go#L190-L202)
- [csp.go:62-90](file://litespaserver/csp.go#L62-L90)
- [csp.go:102-115](file://litespaserver/csp.go#L102-L115)
- [serve.go:223-227](file://litespaserver/serve.go#L223-L227)

### Version Management and Index Caching
- Provider selection: Static provider (development), DB-backed provider (production), or embedded provider (local development).
- Index caching: Per-version cache of rendered index.html to reduce CDN load and improve latency.
- Cache invalidation: Server.FlushCache clears the index cache; Manager.OnChange triggers listeners and cache flushes.

**Section sources**
- [version.go:91-120](file://litespaserver/version.go#L91-L120)
- [serve.go:85-91](file://litespaserver/serve.go#L85-L91)
- [serve.go:204-221](file://litespaserver/serve.go#L204-L221)

## Dependency Analysis
The static file serving stack exhibits low coupling and clear separation of concerns:
- Server depends on staticRetriever, fetcher, Manager, and CSP utilities.
- staticRetriever depends on http.Client, singleflight, and the doublestar library for glob pattern matching.
- Manager encapsulates version sourcing and validation.
- DAO provides persistence for the version key-value store.

```mermaid
classDiagram
class Server {
+ServeRoot(w, r)
-setBaseHeaders(w, nonce)
-indexLookup(version)
-indexStore(version, body)
}
class staticRetriever {
-client
-patterns
-fileCache
+isStatic(path) bool
+retrieve(ctx, cdn, version, path) string
}
class fetcher {
-client
+fetch(ctx, cdn, version) (fetchedVersion, bool)
}
class Manager {
-provider
+Version(ctx) string
+ForceRefresh(ctx)
+SetVersion(ctx, candidate) bool
}
class dao {
-pool
+getVersion(ctx) string
+setVersion(ctx, version) error
}
class CSP {
+cspRule(config, nonce) string
+generateNonce(n) string
}
class Doublestar {
+Match(pattern, path) bool
}
Server --> staticRetriever : "uses"
Server --> fetcher : "uses"
Server --> Manager : "uses"
Manager --> dao : "uses"
Server --> CSP : "uses"
staticRetriever --> Doublestar : "uses"
```

**Diagram sources**
- [serve.go:29-43](file://litespaserver/serve.go#L29-L43)
- [static.go:19-28](file://litespaserver/static.go#L19-L28)
- [fetcher.go:13-15](file://litespaserver/fetcher.go#L13-L15)
- [version.go:80-89](file://litespaserver/version.go#L80-L89)
- [dao.go:24-26](file://litespaserver/dao.go#L24-L26)
- [csp.go:62-90](file://litespaserver/csp.go#L62-L90)
- [static.go:11](file://litespaserver/static.go#L11)

**Section sources**
- [serve.go:29-43](file://litespaserver/serve.go#L29-L43)
- [static.go:19-28](file://litespaserver/static.go#L19-L28)
- [fetcher.go:13-15](file://litespaserver/fetcher.go#L13-L15)
- [version.go:80-89](file://litespaserver/version.go#L80-L89)
- [dao.go:24-26](file://litespaserver/dao.go#L24-L26)
- [csp.go:62-90](file://litespaserver/csp.go#L62-L90)

## Performance Considerations
- Static file cache:
  - Capacity-bound cache reduces repeated CDN fetches.
  - Eviction policy removes an arbitrary entry when capacity is reached.
- Index.html cache:
  - Per-version caching reduces CDN load and improves response times.
  - Eviction policy ensures memory remains bounded.
- Single-flight:
  - Collapses concurrent requests for the same URL or version into a single upstream call.
- Embedded mode:
  - Eliminates network latency and external dependencies for local development.
- MIME type handling:
  - When serving from embedded FS, the server sets Content-Type based on the file extension to enable browser caching and compression.
- Glob pattern matching:
  - Pattern matching uses efficient doublestar library with minimal overhead.
  - Pattern compilation occurs once during initialization.

**Updated** Added performance considerations for glob pattern matching.

## Troubleshooting Guide
Common issues and debugging techniques:
- Static file returns 404:
  - Verify the path matches any pattern in StaticPaths (supports '*', '**', and exact matches).
  - Confirm the path is absolute and begins with a leading slash.
  - Check that glob patterns are correctly formatted (e.g., '/assets/**' not '/assets/*').
- Static file returns 502 Bad Gateway:
  - Indicates upstream CDN failure; check CDN availability and path correctness.
- Index.html not served:
  - Ensure the embedded FS contains index.html at the root when using embedded mode.
  - Validate that the CDN returns a page containing the configured CDN prefix.
- CSP nonce not applied:
  - Static responses do not include a nonce; only index.html responses include a nonce.
  - Verify that the request accepts text/html and is not JSON.
- Version changes not reflected:
  - Call Server.RefreshVersion or Manager.ForceRefresh to reload the version.
  - Ensure Manager.OnChange listeners are registered to trigger cache flushes.
- Glob pattern not working:
  - Single-segment '*' patterns do not cross directory boundaries (use '**' for recursive matching).
  - Check for malformed patterns (e.g., unterminated brackets).
  - Verify pattern order in StaticPaths (more specific patterns first).

**Updated** Added troubleshooting guidance for glob pattern issues.

**Section sources**
- [serve.go:109-136](file://litespaserver/serve.go#L109-L136)
- [serve.go:122-127](file://litespaserver/serve.go#L122-L127)
- [serve.go:61-75](file://litespaserver/serve.go#L61-L75)
- [serve.go:185-187](file://litespaserver/serve.go#L185-L187)
- [serve.go:77-80](file://litespaserver/serve.go#L77-L80)
- [serve.go:85-91](file://litespaserver/serve.go#L85-L91)
- [static_test.go:49-70](file://litespaserver/static_test.go#L49-L70)

## Conclusion
The Lite SPA Server's static file serving is designed for simplicity and safety with enhanced flexibility:
- StaticPaths now supports glob patterns for flexible static file serving scenarios.
- Enhanced allow-list with support for exact matches, single-segment '*' patterns, and recursive '**' patterns.
- Embedded mode enables fast local development without CDN dependencies.
- Robust caching and single-flight mechanisms optimize performance and reduce upstream load.
- Index.html validation and CSP nonce injection ensure correctness and security.

**Updated** Enhanced conclusion to reflect glob pattern support improvements.

## Appendices

### Configuration Examples with Glob Patterns
- Configure StaticPaths with glob patterns:
  - Exact file: "/unsubscribed.html"
  - Single-segment pattern: "/assets/*" (matches one level deep)
  - Recursive pattern: "/assets/**" (matches any depth)
  - Mixed patterns: ["/unsubscribed.html", "/assets/*", "/static/**"]
- Enable EmbeddedContent to serve files directly from a filesystem.
- Set CDNPrefix and DefaultVersion for production deployments.
- Optionally lock CDNVersion for pinned releases.

**Updated** Added examples demonstrating glob pattern configurations.

**Section sources**
- [litespaserver.go:13-41](file://litespaserver/litespaserver.go#L13-L41)
- [static_test.go:24-47](file://litespaserver/static_test.go#L24-L47)
- [static_test.go:49-70](file://litespaserver/static_test.go#L49-L70)
- [serve_test.go:287-311](file://litespaserver/serve_test.go#L287-L311)

### Handling Different File Types with Glob Patterns
- Static files: Served via CDN proxy or embedded FS depending on configuration and glob pattern matching.
- Index.html: Always served with a fresh CSP nonce and base security headers.
- JSON requests: Explicitly rejected with 404.
- Pattern matching behavior:
  - Single-segment '*' matches files in the specified directory only
  - Recursive '**' matches files at any depth within the specified directory
  - Exact matches work as before

**Updated** Added documentation for glob pattern behavior with different file types.

**Section sources**
- [serve.go:93-188](file://litespaserver/serve.go#L93-L188)
- [serve_test.go:30-42](file://litespaserver/serve_test.go#L30-L42)
- [static_test.go:24-47](file://litespaserver/static_test.go#L24-L47)
- [static_test.go:49-70](file://litespaserver/static_test.go#L49-L70)

### Optimizing Static Asset Delivery with Glob Patterns
- Use specific patterns to minimize CDN traffic (e.g., "/assets/**" instead of broad patterns).
- Use embedded mode for local development to avoid network overhead.
- Monitor cache hit rates and adjust capacities if needed.
- Ensure Content-Type headers are set appropriately for embedded files to enable browser caching.
- Order patterns strategically (more specific patterns first).
- Use recursive patterns sparingly for performance optimization.

**Updated** Added optimization guidance for glob pattern usage.

**Section sources**
- [static.go:14-15](file://litespaserver/static.go#L14-L15)
- [serve.go:115-118](file://litespaserver/serve.go#L115-L118)
- [static_test.go:24-47](file://litespaserver/static_test.go#L24-L47)
- [static_test.go:49-70](file://litespaserver/static_test.go#L49-L70)

### Example Assets with Glob Patterns
- Embedded index.html demonstrates nonce placeholder replacement and CSP header injection.
- Embedded unsubscribed.html demonstrates static file serving from embedded FS.
- Embedded app.js demonstrates glob pattern matching for "/assets/*" pattern.

**Updated** Added example demonstrating glob pattern functionality.

**Section sources**
- [index.html:1-6](file://litespaserver/testdata/embed/index.html#L1-L6)
- [unsubscribed.html:1-2](file://litespaserver/testdata/embed/unsubscribed.html#L1-L2)
- [serve_test.go:287-311](file://litespaserver/serve_test.go#L287-L311)