//go:build integration

package postgres

// This file contains integration tests that reproduce the Atlas duplicate-version
// crash at the executor level. They bypass embedDir() (where the guard lives)
// to demonstrate what happens inside ariga.io/atlas when two files share a version.
//
// The guard in embedDir() prevents this scenario from ever reaching the executor.
// See TestEmbedDir_RejectsDuplicateVersion for the guard-level test.

import (
	"context"
	"fmt"
	"testing"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/postgres"
	"github.com/jackc/pgx/v5/stdlib"
)

func mustWriteFile(t *testing.T, dir *migrate.MemDir, name, content string) {
	t.Helper()
	if err := dir.WriteFile(name, []byte(content)); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// writeChecksum computes the atlas.sum from the files currently in the dir.
func writeChecksum(t *testing.T, dir *migrate.MemDir) {
	t.Helper()
	files, err := dir.Files()
	if err != nil {
		t.Fatalf("dir.Files: %v", err)
	}
	sum, err := migrate.NewHashFile(files)
	if err != nil {
		t.Fatalf("NewHashFile: %v", err)
	}
	if err := migrate.WriteSumFile(dir, sum); err != nil {
		t.Fatalf("WriteSumFile: %v", err)
	}
}

// TestRepro_DuplicateVersionPanic confirms that two files sharing the same
// version prefix cause the Atlas executor to panic with "index out of range
// [0] with length 0". This is the exact crash observed in production.
// The embedDir() guard prevents this from happening in practice.
func TestRepro_DuplicateVersionPanic(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, "postgres:tc:")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	driver, err := postgres.Open(sqlDB)
	if err != nil {
		t.Fatalf("open atlas driver: %v", err)
	}

	dir := migrate.OpenMemDir("dupver_repro")
	mustWriteFile(t, dir, "20260712100000_first.sql",
		`CREATE TABLE IF NOT EXISTS repro_a (id int);`)
	mustWriteFile(t, dir, "20260712100000_second.sql",
		`CREATE TABLE IF NOT EXISTS repro_b (id int);`)
	writeChecksum(t, dir)

	rrw := newPGRevisions(sqlDB)
	if err := rrw.init(ctx); err != nil {
		t.Fatalf("init revisions: %v", err)
	}

	executor, err := migrate.NewExecutor(driver, dir, rrw, migrate.WithAllowDirty(true))
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	pending, err := executor.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	t.Logf("pending count: %d", len(pending))

	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("PANIC: %v", r)
			}
		}()
		for _, f := range pending {
			if e := executor.Execute(ctx, f); e != nil {
				runErr = fmt.Errorf("Execute %s: %w", f.Name(), e)
				return
			}
			t.Logf("applied: %s", f.Name())
		}
	}()

	if runErr == nil {
		t.Fatal("expected panic/error for duplicate version, got nil")
	}
	t.Logf("RESULT: %v", runErr)
}

// TestRepro_UniqueVersionOK confirms that the same two files with unique
// versions apply cleanly (no panic, no error).
func TestRepro_UniqueVersionOK(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, "postgres:tc:")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	driver, err := postgres.Open(sqlDB)
	if err != nil {
		t.Fatalf("open atlas driver: %v", err)
	}

	dir := migrate.OpenMemDir("uniqver_repro")
	mustWriteFile(t, dir, "20260712100000_first.sql",
		`CREATE TABLE IF NOT EXISTS repro_a (id int);`)
	mustWriteFile(t, dir, "20260712110000_second.sql",
		`CREATE TABLE IF NOT EXISTS repro_b (id int);`)
	writeChecksum(t, dir)

	rrw := newPGRevisions(sqlDB)
	if err := rrw.init(ctx); err != nil {
		t.Fatalf("init revisions: %v", err)
	}

	executor, err := migrate.NewExecutor(driver, dir, rrw, migrate.WithAllowDirty(true))
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	pending, err := executor.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}

	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("PANIC: %v", r)
			}
		}()
		for _, f := range pending {
			if e := executor.Execute(ctx, f); e != nil {
				runErr = fmt.Errorf("Execute %s: %w", f.Name(), e)
				return
			}
		}
	}()

	if runErr != nil {
		t.Fatalf("unexpected error with unique versions: %v", runErr)
	}
	t.Log("all migrations applied with unique versions")
}
