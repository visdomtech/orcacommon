package postgres

import "testing"

func TestIsEmbeddedPostgres(t *testing.T) {
	tests := []struct {
		dbURL string
		want  bool
	}{
		{"postgres:embedded:", true},
		{"postgres:embedded:?datapath=/tmp", true},
		{"postgres://user:pass@host/db", false},
		{"postgres:tc:", false},
		{"", false},
		// With HasPrefix, a URL containing the prefix mid-string must NOT match.
		{"notpostgres:embedded:", false},
	}
	for _, tt := range tests {
		if got := IsEmbeddedPostgres(tt.dbURL); got != tt.want {
			t.Errorf("IsEmbeddedPostgres(%q) = %v, want %v", tt.dbURL, got, tt.want)
		}
	}
}

func TestIsTestContainer(t *testing.T) {
	tests := []struct {
		dbURL string
		want  bool
	}{
		{"postgres:tc:", true},
		{"postgres:tc:17.5", true},
		{"postgres://user:pass@host/db", false},
		{"postgres:embedded:", false},
		{"", false},
		{"notpostgres:tc:", false},
	}
	for _, tt := range tests {
		if got := IsTestContainer(tt.dbURL); got != tt.want {
			t.Errorf("IsTestContainer(%q) = %v, want %v", tt.dbURL, got, tt.want)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "public", `"public"`},
		{"with space", "my schema", `"my schema"`},
		{"with quote", `sch"ema`, `"sch""ema"`},
		{"empty", "", `""`},
		{"already quoted is not special", `"quoted"`, `"""quoted"""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quoteIdent(tt.in); got != tt.want {
				t.Errorf("quoteIdent(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestParseEmbeddedOptions covers the query-string parser used by the
// "postgres:embedded:" branch of Connect. It exercises happy paths and
// edge cases without requiring a running Postgres instance.
func TestParseEmbeddedOptions(t *testing.T) {
	tests := []struct {
		name         string
		dbURL        string
		wantDataPath string
	}{
		{
			name:         "no query string",
			dbURL:        "postgres:embedded:",
			wantDataPath: "",
		},
		{
			name:         "datapath set",
			dbURL:        "postgres:embedded:?datapath=/tmp/pgdata",
			wantDataPath: "/tmp/pgdata",
		},
		{
			name:         "datapath with spaces escaped",
			dbURL:        "postgres:embedded:?datapath=%2Ftmp%2Fmy+data",
			wantDataPath: "/tmp/my data",
		},
		{
			name:         "unknown params only",
			dbURL:        "postgres:embedded:?foo=bar&baz=qux",
			wantDataPath: "",
		},
		{
			name:         "mixed known and unknown params",
			dbURL:        "postgres:embedded:?foo=bar&datapath=/var/lib/pg&extra=1",
			wantDataPath: "/var/lib/pg",
		},
		{
			name:         "malformed query string",
			dbURL:        "postgres:embedded:?%",
			wantDataPath: "",
		},
		{
			name:         "no embedded prefix",
			dbURL:        "postgres://user:pass@host/db",
			wantDataPath: "",
		},
		{
			name:         "empty datapath value",
			dbURL:        "postgres:embedded:?datapath=",
			wantDataPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEmbeddedOptions(tt.dbURL)
			if got.dataPath != tt.wantDataPath {
				t.Errorf("parseEmbeddedOptions(%q).dataPath = %q, want %q",
					tt.dbURL, got.dataPath, tt.wantDataPath)
			}
		})
	}
}
