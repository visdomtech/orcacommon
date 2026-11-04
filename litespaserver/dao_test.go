package litespaserver

import (
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

func TestVersionKeyRejectsInvalidNames(t *testing.T) {
	invalid := []struct {
		name         string
		frontendName string
	}{
		{"spaces in name", "admin panel"},
		{"leading dot", ".admin"},
		{"trailing dot", "admin."},
		{"consecutive dots", "app..dashboard"},
		{"special characters", "admin;drop"},
		{"semicolon", "foo;bar"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("versionKey(%q) did not panic, want panic", tt.frontendName)
				}
			}()
			versionKey(tt.frontendName)
		})
	}
}
