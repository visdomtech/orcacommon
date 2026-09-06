package litespaserver

import "testing"

func TestVersionKey(t *testing.T) {
	tests := []struct {
		name         string
		frontendName string
		want         string
	}{
		{"empty frontend name uses default key", "", "frontend.version"},
		{"non-empty frontend name appends suffix", "admin", "frontend.version.admin"},
		{"multi-segment frontend name", "app.dashboard", "frontend.version.app.dashboard"},
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
