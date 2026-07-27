# Email Package

Shared Mailgun-backed email sender for transactional email delivery. Zero external dependencies beyond the Go standard library.

## Responsibilities

- Provide a `Sender` interface for email sending (enables mock injection in tests)
- Wrap the Mailgun HTTP API with input validation, error handling, and connection pooling
- Support text/HTML bodies, CC/BCC, and file attachments (up to 10 MB each)

## Usage

```go
import "github.com/visdomtech/orcacommon/email"

client := email.NewMailgunClient(email.MailgunConfig{
    Endpoint: endpoint,
    User:     user,
    Password: password,
    From:     "noreply@example.com", // default "from" address; can be overridden per-message
})
client.Send(ctx, &email.EmailMessage{
    To:      []string{"user@example.com"},
    Subject: "Hello",
    HTML:    "<p>Hello!</p>",
    // From is optional here — falls back to MailgunConfig.From when empty
})
```

## Configuration

Requires the following environment variables in the consuming service:
- `MAILGUN_ENDPOINT` — Mailgun API endpoint
- `MAILGUN_USER` — Mailgun authentication username
- `MAILGUN_PASSWORD` — Mailgun API key
- `MAILGUN_FROM` — Default sender email address (falls back to per-message `EmailMessage.From` if unset)

> **Note:** The `env` struct tags on `MailgunConfig` follow the [caarlos0/env](https://github.com/caarlos0/env) convention.
> The consuming service is responsible for parsing them (e.g. `env.ParseWithOptions(&cfg, env.Options{Prefix: "MAILGUN_"})`).

### `From` address resolution order

1. `EmailMessage.From` (per-message override)
2. `MailgunConfig.From` (client-level default)
3. Error if both are empty

## HTTP Client

`NewMailgunClient` creates a dedicated `*http.Client` with:
- 30-second request timeout
- Connection pooling (10 max idle connections, 5 per host)
- 10-second dial/TLS handshake timeouts
