package postgres

import "testing"

// TestParseEmbeddedOptions covers the query-string parser used by the
// "postgres:embedded:" branch of Connect. It exercises happy paths and
// edge cases without requiring a running Postgres instance.
func TestParseEmbeddedOptions(t *testing.T) {
	tests := []struct {
		name         string
		dbURL        string
		wantDataPath string
		wantDBUser   string
		wantDBPass   string
		wantDBName   string
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
		{
			name:         "all fields set",
			dbURL:        "postgres:embedded:?datapath=/tmp/pg&user=alice&password=secret&name=mydb",
			wantDataPath: "/tmp/pg",
			wantDBUser:   "alice",
			wantDBPass:   "secret",
			wantDBName:   "mydb",
		},
		{
			name:       "only user set",
			dbURL:      "postgres:embedded:?user=bob",
			wantDBUser: "bob",
		},
		{
			name:       "only password set",
			dbURL:      "postgres:embedded:?password=p%40ss",
			wantDBPass: "p@ss",
		},
		{
			name:       "only name set",
			dbURL:      "postgres:embedded:?name=testdb",
			wantDBName: "testdb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEmbeddedOptions(tt.dbURL)
			if got.dataPath != tt.wantDataPath {
				t.Errorf("parseEmbeddedOptions(%q).dataPath = %q, want %q",
					tt.dbURL, got.dataPath, tt.wantDataPath)
			}
			if got.dbUser != tt.wantDBUser {
				t.Errorf("parseEmbeddedOptions(%q).dbUser = %q, want %q",
					tt.dbURL, got.dbUser, tt.wantDBUser)
			}
			if got.dbPassword != tt.wantDBPass {
				t.Errorf("parseEmbeddedOptions(%q).dbPassword = %q, want %q",
					tt.dbURL, got.dbPassword, tt.wantDBPass)
			}
			if got.dbName != tt.wantDBName {
				t.Errorf("parseEmbeddedOptions(%q).dbName = %q, want %q",
					tt.dbURL, got.dbName, tt.wantDBName)
			}
		})
	}
}
