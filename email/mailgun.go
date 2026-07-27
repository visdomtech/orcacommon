// Package email provides a Mailgun-backed email sender for transactional email
// delivery. It is intentionally dependency-free beyond the Go standard library.
package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

const maxAttachmentSize = 10 * 1024 * 1024 // 10 MB

// Sender is the interface satisfied by MailgunClient. It enables mock
// injection in tests and decouples callers from the concrete implementation.
type Sender interface {
	Send(ctx context.Context, msg *EmailMessage) (string, error)
}

// Attachment describes a file to attach to an email. Data is raw file bytes.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
	Inline      bool
}

// EmailMessage represents an email to be sent.
type EmailMessage struct {
	From        string
	To          []string
	CC          []string
	BCC         []string
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment
}

// MailgunConfig holds the configuration for a MailgunClient.
// Environment variables are read with the "MAILGUN_" prefix (e.g. MAILGUN_ENDPOINT, MAILGUN_PASSWORD).
type MailgunConfig struct {
	Endpoint string `env:"ENDPOINT"`
	User     string `env:"USER"`
	Password string `env:"PASSWORD"`
	From     string `env:"FROM" envDefault:"noti@doublefin.com"`
}

// LogValue implements slog.LogValuer, redacting the password.
func (c MailgunConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("endpoint", c.Endpoint),
		slog.String("user", c.User),
		slog.String("password", "[REDACTED]"),
		slog.String("from", c.From),
	)
}

// MailgunClient sends emails via the Mailgun API.
type MailgunClient struct {
	config MailgunConfig
	client *http.Client
}

// NewMailgunClient creates a MailgunClient from a MailgunConfig.
// The returned client uses a dedicated http.Client with a 30-second timeout
// and connection pooling.
func NewMailgunClient(cfg MailgunConfig) *MailgunClient {
	cfg.Endpoint = strings.TrimSuffix(cfg.Endpoint, "/")
	return &MailgunClient{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          10,
				MaxIdleConnsPerHost:   5,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
	}
}

// compile-time check that *MailgunClient implements Sender.
var _ Sender = (*MailgunClient)(nil)

// Send posts the email to Mailgun.
func (c *MailgunClient) Send(ctx context.Context, msg *EmailMessage) (string, error) {
	if c.config.Endpoint == "" || c.config.User == "" || c.config.Password == "" {
		return "", fmt.Errorf("mailgun client not configured: check MAILGUN_ENDPOINT, MAILGUN_USER, MAILGUN_PASSWORD")
	}
	if len(msg.To) == 0 {
		return "", fmt.Errorf("at least one To recipient is required")
	}
	if msg.Text == "" && msg.HTML == "" {
		return "", fmt.Errorf("at least one of text or html body is required")
	}

	for _, a := range msg.Attachments {
		if a.Filename == "" {
			return "", fmt.Errorf("attachment filename is required")
		}
		if a.ContentType == "" {
			return "", fmt.Errorf("attachment contentType is required")
		}
		if len(a.Data) > maxAttachmentSize {
			return "", fmt.Errorf("attachment exceeds size limit (10MB): %s", a.Filename)
		}
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	from := msg.From
	if from == "" {
		from = c.config.From
	}
	if from == "" {
		return "", fmt.Errorf("from address is required: set MailgunConfig.From or EmailMessage.From")
	}

	_ = writer.WriteField("from", from)
	_ = writer.WriteField("to", strings.Join(msg.To, ","))
	_ = writer.WriteField("subject", msg.Subject)
	if msg.Text != "" {
		_ = writer.WriteField("text", msg.Text)
	}
	if msg.HTML != "" {
		_ = writer.WriteField("html", msg.HTML)
	}
	if len(msg.CC) > 0 {
		_ = writer.WriteField("cc", strings.Join(msg.CC, ","))
	}
	if len(msg.BCC) > 0 {
		_ = writer.WriteField("bcc", strings.Join(msg.BCC, ","))
	}

	for _, a := range msg.Attachments {
		fieldName := "attachment"
		if a.Inline {
			fieldName = "inline"
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, a.Filename))
		h.Set("Content-Type", a.ContentType)
		part, err := writer.CreatePart(h)
		if err != nil {
			return "", fmt.Errorf("create attachment part: %w", err)
		}
		if _, err := part.Write(a.Data); err != nil {
			return "", fmt.Errorf("write attachment: %w", err)
		}
	}

	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.Endpoint+"/messages", &body)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.config.User+":"+c.config.Password)))

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("mailgun API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return string(respBody), nil
}
