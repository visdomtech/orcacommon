# Utility Functions

General-purpose helpers: struct↔map conversion (JSON-based and reflection-based), HTTP/network utilities, embedded Postgres process probes, and a split-level slog handler.

## Components

### Struct↔Map Conversion (`convert.go`)

**JSON-based:**
- `StructToMap[T](*T)` — serializes a json-tagged struct pointer to `map[string]any` via `json.Marshal`/`Unmarshal`.
- `MapToStruct[T](map[string]any)` — deserializes a map back into a typed struct pointer.
- `PrettyJSON(v)` — indented JSON for debugging.

**Reflection-based (`StructToMapLC`):**
- Converts PascalCase field names to camelCase.
- Flattens embedded structs into the parent map.
- Respects `json.Marshaler` implementations (uses `MarshalJSON` instead of field reflection).
- Configurable via functional options:
  - `WithIDSuffix()` — `"UserID"` → `"userId"` instead of `"userID"`.
  - `WithOmitEmpty()` — skip empty strings, nil pointers, nil/empty slices.
  - `WithNilMapToEmpty()` — nil map → `{}` instead of `null`.
  - `WithNilSliceToEmpty()` — nil slice → `[]` instead of `null`.

### Network Helpers (`network.go`)
- `IsFromLocalhost(req)` — checks `RemoteAddr` against `127.0.0.1` / `::1`.
- `WriteJSONResponse(w, status, v)` — sets `Content-Type` and writes JSON.
- `RequestHost(req)` — returns `X-Forwarded-Host` or `req.Host`.
- `GetFreePort()` — allocates an unused TCP port on `127.0.0.1` (small race window in high-contention).

### Embedded Postgres Probes (`embedded_pg.go`, `embedded_pg_windows.go`)
Unix-only (build constraint `!windows`). Windows has no-op stubs.

- `IsDataPathInitialized(dataPath)` — checks for `PG_VERSION` file.
- `CheckPIDFile(dataPath)` — reads `postmaster.pid`, probes process liveness via `Signal(0)`.
- `IsPortListening(host, port, timeout)` — TCP dial check.
- `ReadPostmasterPort(dataPath)` — reads port from line 4 of `postmaster.pid`.
- `ReuseEmbeddedPG(dataPath)` — composite check: PID alive AND port listening → `(true, port)`.
- `IsEmbeddedPGRunning(dataPath)` — convenience wrapper around `ReuseEmbeddedPG`.

### Split-Level slog Handler (`slog_handler.go`)
`SplitLevelHandler` routes log records to stdout (below `Error`) and stderr (`Error` and above). Implements `slog.Handler` interface. Composes two child handlers (`StdHandler`, `ErrHandler`).
