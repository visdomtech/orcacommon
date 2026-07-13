package postgres

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmbedDir_RejectsDuplicateVersion verifies that embedDir returns a clear
// error (not a panic) when two .sql files share the same version prefix.
// This prevents the opaque "index out of range [0] with length 0" panic
// inside the Atlas executor.
func TestEmbedDir_RejectsDuplicateVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"20260712100000_first.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE a (id int);")},
		"20260712100000_second.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE b (id int);")},
		"atlas.sum":                  &fstest.MapFile{Data: []byte("h1:placeholder\n")},
	}

	_, err := embedDir(fsys, "test")
	if err == nil {
		t.Fatal("expected error for duplicate version, got nil")
	}

	// The error must mention "duplicate" and the shared version.
	errMsg := err.Error()
	for _, want := range []string{"duplicate", "20260712100000"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error should mention %q, got: %s", want, errMsg)
		}
	}
	t.Logf("guard error: %s", errMsg)
}

// TestEmbedDir_AcceptsUniqueVersions verifies that embedDir succeeds when all
// .sql files have unique version prefixes.
func TestEmbedDir_AcceptsUniqueVersions(t *testing.T) {
	fsys := fstest.MapFS{
		"20260712100000_first.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE a (id int);")},
		"20260712110000_second.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE b (id int);")},
		"atlas.sum":                  &fstest.MapFile{Data: []byte("h1:placeholder\n")},
	}

	dir, err := embedDir(fsys, "test")
	if err != nil {
		t.Fatalf("embedDir with unique versions: %v", err)
	}

	files, err := dir.Files()
	if err != nil {
		t.Fatalf("dir.Files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 migration files, got %d", len(files))
	}
	t.Logf("loaded %d files with unique versions", len(files))
}

// TestVersionPrefix checks the version prefix extractor.
func TestVersionPrefix(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"20260712100000_desc.sql", "20260712100000"},
		{"20260705100001_orca.seed.sql", "20260705100001"},
		{"atlas.sum", ""},
		{"noseparator.sql", ""},
	}
	for _, tt := range tests {
		if got := versionPrefix(tt.file); got != tt.want {
			t.Errorf("versionPrefix(%q) = %q, want %q", tt.file, got, tt.want)
		}
	}
}
