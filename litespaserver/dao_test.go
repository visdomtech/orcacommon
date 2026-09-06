package litespaserver

import (
	"context"
	"testing"
)

func TestVersionKey(t *testing.T) {
	tests := []struct {
		name         string
		frontendName string
		want         string
	}{
		{"empty frontend name uses default key", "", "frontend.version"},
		{"non-empty frontend name appends suffix", "admin", "frontend.version.admin"},
		{"multi-segment frontend name", "app.dashboard", "frontend.version.app.dashboard"},
		{"whitespace-only treated as empty", "   ", "frontend.version"},
		{"leading/trailing whitespace trimmed", " admin ", "frontend.version.admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := versionKey(tt.frontendName)
			if got != tt.want {
				t.Errorf("versionKey(%q) = %q, want %q", tt.frontendName, got, tt.want)
			}
		})
	}
}

func TestDaoKeyField(t *testing.T) {
	// Verify that the dao struct carries the key field and that it is
	// computed at construction time by versionKey.
	d := &dao{key: versionKey("admin")}
	if d.key != "frontend.version.admin" {
		t.Errorf("dao.key = %q, want %q", d.key, "frontend.version.admin")
	}

	d2 := &dao{key: versionKey("")}
	if d2.key != "frontend.version" {
		t.Errorf("dao.key = %q, want %q", d2.key, "frontend.version")
	}
}

func TestNewManagerWiresFrontendName(t *testing.T) {
	// Verify that NewManager correctly threads FrontendName from Config
	// into dao.key. Use CDNVersion to avoid DB access.
	ctx := context.Background()

	tests := []struct {
		name         string
		frontendName string
		wantKey      string
	}{
		{"empty frontend name", "", "frontend.version"},
		{"non-empty frontend name", "admin", "frontend.version.admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				CDNPrefix:    "https://cdn.example.com",
				CDNVersion:   "v1.0.0", // locks version, no DB needed
				FrontendName: tt.frontendName,
			}
			m := NewManager(ctx, nil, cfg)
			if m.dao.key != tt.wantKey {
				t.Errorf("Manager.dao.key = %q, want %q", m.dao.key, tt.wantKey)
			}
			if m.Version(ctx) != "v1.0.0" {
				t.Errorf("Manager.Version() = %q, want %q", m.Version(ctx), "v1.0.0")
			}
		})
	}
}
