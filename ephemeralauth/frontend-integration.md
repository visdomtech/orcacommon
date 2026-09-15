# Frontend Integration Contract

This document describes how frontend applications interact with the ephemeral auth system. It covers the complete lifecycle: guest session bootstrapping, Turnstile challenge solving, token issuance, API authentication, and token renewal.

## Architecture Overview

The ephemeral auth system involves **two distinct tokens** operating at different layers:

| Token | Purpose | Produced by | Consumed by |
|-------|---------|-------------|-------------|
| **Turnstile token** (`bot_token`) | One-time proof that the visitor solved a bot challenge | Cloudflare Turnstile JS widget (client-side) | Your server's issuance endpoint, which forwards it to Cloudflare's `siteverify` API |
| **Ephemeral JWT** (`token`) | Short-lived, scoped API access credential bound to the client's session, IP, and User-Agent | Your server via `IssuanceHandler` | Your server's `Protect` middleware on subsequent API calls |

The Turnstile token is consumed and discarded during issuance — it serves only as a one-time gate. The ephemeral JWT is what the frontend actually uses for API calls.

### Request Flow Diagram

```
┌─────────┐                  ┌──────────┐                    ┌────────────┐
│ Browser  │                  │  Server  │                    │ Cloudflare │
└────┬─────┘                  └────┬─────┘                    └─────┬──────┘
     │                             │                                │
     │  1. GET / (page load)       │                                │
     ├────────────────────────────►│                                │
     │  Set-Cookie: guest_session  │                                │
     │◄────────────────────────────┤                                │
     │                             │                                │
     │  2. Turnstile widget solves │                                │
     │     challenge (client-side) │                                │
     │  → produces bot_token       │                                │
     │                             │                                │
     │  3. POST /api/auth/         │                                │
     │     ephemeral-token         │                                │
     │     { bot_token, cookie }   │                                │
     ├────────────────────────────►│  4. POST siteverify            │
     │                             ├───────────────────────────────►│
     │                             │     { success: true }          │
     │                             │◄───────────────────────────────┤
     │                             │                                │
     │                             │  5. Issue JWT (HS256)          │
     │  { token, expires_in }      │     bound to session/IP/UA     │
     │◄────────────────────────────┤                                │
     │                             │                                │
     │  6. GET /api/public/data    │                                │
     │     Authorization: Bearer   │                                │
     ├────────────────────────────►│  7. Verify JWT + bindings      │
     │     200 OK                  │     + scopes (stateless)       │
     │◄────────────────────────────┤                                │
```

## Prerequisites

### Guest Session Cookie

The `GuestSession` middleware sets an HMAC-signed cookie (`guest_session`) on the first page load. This cookie:

- Is `SameSite=Strict; HttpOnly; Secure` — JavaScript cannot read it directly
- Contains a 16-byte random session ID signed with HMAC-SHA256
- Must be present on every request to the token endpoint and protected API routes

**Frontend requirement:** All `fetch` calls must include credentials so the browser sends the cookie:

```js
// fetch API
fetch(url, { credentials: "include" });

// XMLHttpRequest
xhr.withCredentials = true;
```

This requires same-origin requests. If the frontend and API are on different origins, the cookie will not be sent and issuance will fail with `401`.

### Cloudflare Turnstile Widget

Embed the Turnstile widget in your HTML to produce `bot_token` values:

```html
<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>

<div class="cf-turnstile"
     data-sitekey="<TURNSTILE_SITEKEY>"
     data-callback="onTurnstileSuccess">
</div>

<script>
  function onTurnstileSuccess(token) {
    // `token` is the bot_token to send to the issuance endpoint
  }
</script>
```

The `TURNSTILE_SITEKEY` is the **public** key from `Config.TurnstileSitekey` — it is safe to embed in client-side code. The `TurnstileSecret` is server-side only and must never be exposed to the frontend.

For local development, use Cloudflare's official dummy test keys (always-pass sitekey `1x00000000000000000000AA`) so the widget resolves instantly without real challenges.

## Token Issuance Endpoint

### Request

```
POST /api/auth/ephemeral-token
Content-Type: application/json

{
  "bot_token": "<turnstile-response-token>"
}
```

**Constraints:**
- Method must be `POST` — all other methods return `405`
- Request body is limited to **1 KB** via `http.MaxBytesReader` — larger bodies are rejected
- The `bot_token` field is required; omitting it returns `400`
- The guest session cookie must be present and valid; missing/invalid cookies return `401`

### Success Response (200)

```json
{
  "token": "<signed-JWT>",
  "expires_in": 120
}
```

| Field | Type | Description |
|-------|------|-------------|
| `token` | string | Opaque JWT string. Do not decode, inspect, or modify. |
| `expires_in` | number | Token lifetime in seconds (always 60–180). After this duration, the token is invalid. |

### Error Responses

| Status | Cause | Frontend action |
|--------|-------|-----------------|
| `400` | Missing/malformed `bot_token` or invalid JSON body | Check the request payload; ensure `bot_token` is a non-empty string from the Turnstile callback |
| `413` | Request body exceeds 1 KB limit | Ensure no extra fields are sent beyond `bot_token` |
| `401` | No valid guest session cookie | Load/reload a page served through `GuestSession` middleware to bootstrap the cookie, then retry |
| `403` | Bot verification failed (Cloudflare rejected the token) | The Turnstile token may be expired or duplicate — re-render the widget and solve again |
| `405` | Wrong HTTP method | Ensure the request uses `POST` |

## Using the Token on API Calls

Include the JWT in the `Authorization` header on every protected API request:

```
Authorization: Bearer <token>
```

### What the Server Validates

The `Protect` middleware performs six checks on every request, in order:

1. **Bearer token present** — missing or malformed `Authorization` header → `401`
2. **Signature + expiry** — HS256 signature verification and `exp` claim check (stateless, no server-side store) → `401`
3. **Session binding** — the JWT's `sub` claim must match the current guest session cookie → `403`
4. **IP binding** — HMAC hash of the client IP must match the `ip_hash` claim → `403`
5. **User-Agent binding** — HMAC hash of the `User-Agent` header must match the `ua_hash` claim → `403`
6. **Scope enforcement** — every scope in `RequiredScopes` must be present in the token's `scopes` claim → `403`

**Implications for the frontend:**
- The token only works from the same IP address and browser that obtained it
- Switching networks (Wi-Fi → cellular), using a VPN, or going through a proxy that changes `X-Forwarded-For` will invalidate the token
- The token cannot be shared across devices or tabs with different User-Agent strings

## Token Renewal (Refresh Flow)

Tokens are short-lived (60–180 seconds) and there is **no silent refresh path**. Each renewal requires a fresh Turnstile challenge solve, which re-proves humanity.

### When to Renew

Renew **only** when a protected API call returns `401`. Do not proactively refresh based on a timer — the server is the authority on token validity.

### Renewal Sequence

```
401 received → Re-solve Turnstile → POST new bot_token → Retry original call (once)
```

Step by step:

1. Protected API call returns `401`
2. Re-render the Turnstile widget and wait for a new `bot_token` from the callback
3. `POST /api/auth/ephemeral-token` with the new `bot_token` and the guest cookie
4. Retry the original API call with the freshly issued token
5. If the retry also fails — **stop**. Do not retry again without user interaction

### Reference Implementation

```js
let currentToken = null;

async function solveTurnstile() {
  return new Promise((resolve) => {
    // Render or re-render the Turnstile widget
    turnstile.render("#turnstile-container", {
      sitekey: TURNSTILE_SITEKEY,
      callback: (token) => resolve(token),
    });
  });
}

async function refreshToken() {
  const botToken = await solveTurnstile();
  const res = await fetch("/api/auth/ephemeral-token", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ bot_token: botToken }),
  });

  if (!res.ok) {
    throw new Error(`Token issuance failed: ${res.status}`);
  }

  const { token, expires_in } = await res.json();
  currentToken = token;
  return token;
}

async function apiCall(url, options = {}) {
  // Ensure we have a token
  if (!currentToken) {
    await refreshToken();
  }

  // First attempt
  options.headers = {
    ...options.headers,
    Authorization: `Bearer ${currentToken}`,
  };
  let res = await fetch(url, { ...options, credentials: "include" });

  if (res.status === 401) {
    // Token expired or binding changed — renew and retry ONCE
    await refreshToken();
    options.headers.Authorization = `Bearer ${currentToken}`;
    res = await fetch(url, { ...options, credentials: "include" });
  }

  // If still failing after retry, return the response as-is.
  // Do NOT loop or retry further.
  return res;
}
```

### Why No Silent Refresh?

Unlike OAuth refresh tokens, there is intentionally no background renewal mechanism. The design trade-off is:

- **Pro:** Every token renewal re-verifies the visitor is human, preventing bots from obtaining an indefinite stream of tokens from a single solved challenge
- **Con:** The Turnstile widget must briefly appear (or re-run invisibly) on each renewal, which may cause a short UI interruption

## Token Handling Rules

| Rule | Rationale |
|------|-----------|
| Treat tokens as opaque strings | The JWT structure is a server implementation detail; the frontend should never parse or depend on claim names |
| Do not decode the JWT client-side | Claim names or signing algorithms may change; decoding creates a fragile coupling |
| Do not cache tokens beyond `expires_in` | Tokens are designed to be short-lived; caching defeats the purpose |
| Do not store tokens in `localStorage` | Prefer holding tokens in memory only; `localStorage` persists across tabs and sessions and is accessible to any XSS payload |
| Include `credentials: "include"` on every fetch | The guest cookie is required for both issuance and protected API calls |
| Never expose the Turnstile secret | Only the sitekey belongs in frontend code; the secret is server-side only |

## Development and Testing

### Local Development Setup

Use Cloudflare's dummy test keys to avoid real challenges during development:

```
TURNSTILE_SITEKEY=1x00000000000000000000AA    # always passes
TURNSTILE_SECRET=1x0000000000000000000000000000000AA
```

The always-pass sitekey causes the Turnstile widget callback to fire immediately without any user interaction, making it suitable for automated frontend tests.

### Available Test Keys

| Key | Sitekey | Secret | Behavior |
|-----|---------|--------|----------|
| Always pass | `1x00000000000000000000AA` | `1x0000000000000000000000000000000AA` | Challenge always succeeds |
| Always fail | `2x00000000000000000000AB` | `2x0000000000000000000000000000000AA` | Challenge always fails |
| Force interactive | `3x00000000000000000000FF` | — | Forces visible challenge (requires human) |
| Token expired | — | `3x0000000000000000000000000000000AA` | Returns `timeout-or-duplicate` error |

### E2E Testing Pattern

```js
// In your E2E test setup, inject a mock Turnstile callback
// that immediately returns a dummy token, then verify the
// full issuance → API call flow works end-to-end.
```

## Security Considerations for Frontend Developers

- **Context binding prevents token sharing:** Even if a token is intercepted, it cannot be used from a different IP address or browser. This is enforced server-side via HMAC hashes in the JWT claims.
- **Fail-closed bot verification:** If Cloudflare's siteverify API is unreachable or returns an error, the server rejects issuance. The frontend should surface a user-friendly message and allow retry.
- **No server-side session store:** The system is fully stateless. There is no way to revoke an individual token — it simply expires within 60–180 seconds. This is by design to keep the infrastructure simple and horizontally scalable.
- **1 KB body limit:** The issuance endpoint rejects request bodies larger than 1 KB. Do not send additional fields beyond `bot_token`.
