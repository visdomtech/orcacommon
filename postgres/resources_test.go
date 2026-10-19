//go:build !windows

package postgres

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cloud.google.com/go/cloudsqlconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/puddle/v2"
	"github.com/visdomtech/orcacommon/utils"
)

// attachTestResource records a resource under an existing entry, standing in for
// the Cloud SQL dialer or TestContainer that connect() would have provisioned.
func attachTestResource(t *testing.T, key, name string, release func(context.Context) error) {
	t.Helper()
	e := poolRegistry.entryFor(key)
	if e == nil {
		t.Fatalf("no registry entry for %q", key)
	}
	poolRegistry.attachAcquired(e, name, release)
}

// TestCloseReleasesEveryAcquiredResource pins the symmetry an open has to keep:
// what a generation acquired alone comes back, most recent first, and only after
// the pool that used it is gone. A release that stops at the first failure would
// strand the rest of the set, so both are attempted.
func TestCloseReleasesEveryAcquiredResource(t *testing.T) {
	const key = "release-acquired-key"
	ctx := t.Context()

	pool := lazyPoolFor(t, key)
	var released []string
	var pingAtRelease error

	attachTestResource(t, key, "cloudsql dialer", func(context.Context) error {
		released = append(released, "cloudsql dialer")
		return nil
	})
	attachTestResource(t, key, "testcontainer", func(context.Context) error {
		released = append(released, "testcontainer")
		pingAtRelease = pool.Ping(ctx)
		return errors.New("docker went away")
	})

	if err := ClosePoolWithKey(ctx, key); err != nil {
		t.Fatalf("ClosePoolWithKey: %v", err)
	}

	if want := []string{"testcontainer", "cloudsql dialer"}; !slices.Equal(released, want) {
		t.Errorf("released %v, want %v (last acquired first, and every one of them)", released, want)
	}
	if !errors.Is(pingAtRelease, puddle.ErrClosedPool) {
		t.Errorf("pool at release time = %v, want it already released", pingAtRelease)
	}

	// The entry is emptied as it is released: a second sweep must not run the
	// same resource again.
	if err := ClosePoolWithKey(ctx, key); err != nil {
		t.Fatalf("second ClosePoolWithKey: %v", err)
	}
	if n := len(released); n != 2 {
		t.Errorf("%d releases after a second close, want the original 2", n)
	}
}

// recordingDialer stands in for the Cloud SQL connector, counting how many times
// it was given back and what the pool looked like when that happened.
type recordingDialer struct {
	closes        int
	poolAtClosing error
	pool          *pgxpool.Pool
}

func (d *recordingDialer) Dial(context.Context, string, ...cloudsqlconn.DialOption) (net.Conn, error) {
	return nil, errors.New("no Cloud SQL instance in a unit test")
}

func (d *recordingDialer) Close() error {
	d.closes++
	if d.pool != nil {
		d.poolAtClosing = d.pool.Ping(context.Background())
	}
	return nil
}

// TestClosePoolWithKeyClosesTheCloudSQLDialer covers the connector half of
// acquisition symmetry. A cached pool keeps its dialer reachable, so an
// unreleased dialer is a live connector with a dead pool behind it; Close is the
// only way back, and it has to happen after the pool it serves.
func TestClosePoolWithKeyClosesTheCloudSQLDialer(t *testing.T) {
	const key = "cloudsql-dialer-key"
	ctx := t.Context()

	dialer := &recordingDialer{}
	original := newCloudDialer
	newCloudDialer = func(context.Context) (cloudDialer, error) { return dialer, nil }
	t.Cleanup(func() { newCloudDialer = original })

	pool, err := OpenPoolWithKey(ctx, DBConfig{
		CloudSQLInstance: "test-project:test-region:test-instance",
		User:             "u",
		Password:         "p",
		Name:             "n",
	}, nil, key)
	if err != nil {
		t.Fatalf("OpenPoolWithKey: %v", err)
	}
	dialer.pool = pool

	if n := dialer.closes; n != 0 {
		t.Fatalf("the open pool's dialer was closed %d times already", n)
	}

	if err := ClosePoolWithKey(ctx, key); err != nil {
		t.Fatalf("ClosePoolWithKey: %v", err)
	}
	if n := dialer.closes; n != 1 {
		t.Errorf("dialer closed %d times, want exactly once per release", n)
	}
	if !errors.Is(dialer.poolAtClosing, puddle.ErrClosedPool) {
		t.Errorf("pool at dialer close = %v, want it already released", dialer.poolAtClosing)
	}

	// A reopen provisions its own connector rather than resurrecting this one.
	if _, err := OpenPoolWithKey(ctx, DBConfig{
		CloudSQLInstance: "test-project:test-region:test-instance",
		User:             "u",
		Password:         "p",
		Name:             "n",
	}, nil, key); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = ClosePoolWithKey(ctx, key) })
	if n := dialer.closes; n != 1 {
		t.Errorf("the reopen closed the previous generation's dialer again (closes=%d)", n)
	}
}

// TestStopReleasesTheDirectoriesAnInstanceCreated covers the disk half of the
// same symmetry: an auto-created data directory (~47 MB) and the runtime
// directory that held its initdb password file go away with the server, a data
// path the caller named stays, and neither is deleted while the postmaster could
// still be running underneath it.
func TestStopReleasesTheDirectoriesAnInstanceCreated(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		dataPath := t.TempDir()
		runtimePath := t.TempDir()
		seedFile(t, filepath.Join(runtimePath, "pwfile"), "hunter2")

		stopWithForceKill("stop-owns-dirs", embeddedInstance{
			dataPath:     dataPath,
			ownsDataPath: true,
			runtimePath:  runtimePath,
			pid:          deadPID(t),
		})

		assertGone(t, dataPath)
		assertGone(t, runtimePath)
	})

	t.Run("callerNamedDataPathSurvives", func(t *testing.T) {
		dataPath := t.TempDir()
		runtimePath := t.TempDir()

		stopWithForceKill("stop-named-datapath", embeddedInstance{
			dataPath:    dataPath,
			runtimePath: runtimePath,
			pid:         deadPID(t),
		})

		assertGone(t, runtimePath)
		if _, err := os.Stat(dataPath); err != nil {
			t.Errorf("the data directory the caller named was deleted: %v", err)
		}
	})

	t.Run("unconfirmedStopBlocksDeletion", func(t *testing.T) {
		// The pinned postmaster is this test process: alive, and not a Postgres, so
		// the stop path must neither signal it nor delete the directory it serves.
		dataPath := t.TempDir()
		inst := postmasterPIDFile(t, dataPath, os.Getpid(), 5432)
		inst.ownsDataPath = true

		stopWithForceKill("stop-unconfirmed", inst)

		if _, err := os.Stat(dataPath); err != nil {
			t.Errorf("a data directory was deleted without confirming its postmaster stopped: %v", err)
		}
	})
}

// TestFailedEmbeddedStartReleasesWhatItCreated drives the real provisioning
// failure branch: initdb gets as far as writing the password file into the
// per-key runtime directory, then fails, and the residue — a plaintext password
// file — must not stay behind in the shared cache.
func TestFailedEmbeddedStartReleasesWhatItCreated(t *testing.T) {
	const key = "failed-embedded-start-key"

	if _, err := os.Stat(filepath.Join(embeddedBinariesPath(), "bin", "pg_ctl")); err != nil {
		t.Skip("the Postgres binaries are not extracted yet, so initdb cannot be reached")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unwritable directory would still be writable")
	}

	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("make the data path's parent unwritable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	runtimePath := embeddedRuntimePath(key)
	if err := os.MkdirAll(runtimePath, 0o700); err != nil {
		t.Fatalf("seed the runtime directory: %v", err)
	}

	_, err := Connect(t.Context(), "postgres:embedded:?datapath="+filepath.Join(parent, "pgdata"), key)
	if err == nil {
		_ = ClosePoolWithKey(t.Context(), key)
		t.Fatal("expected initdb to fail against an unwritable parent directory")
	}
	// Pin the branch: the provision has to have failed inside initdb, which is
	// after the password file was written, rather than in an earlier step that
	// would leave nothing to clean up.
	if !strings.Contains(err.Error(), "unable to init database") {
		t.Fatalf("expected an initdb failure, got: %v", err)
	}

	// Start() recreates the runtime directory before it fails, so this is the
	// branch that has to remove it rather than prove it was never made.
	assertGone(t, runtimePath)
	if _, registered := registeredInstance(t, key); registered {
		t.Error("a failed provision left an embedded instance registered")
	}
}

func seedFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", filepath.Base(path), err)
	}
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s still exists (stat error: %v)", filepath.Base(path), err)
	}
}

// lazyPoolFor creates a minimal registry entry with a real pgxpool.Pool for a
// key, used by tests that attach resources without opening a real database.
func lazyPoolFor(t *testing.T, key string) *pgxpool.Pool {
	t.Helper()
	entry := poolRegistry.newEntry(key)
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/d?connect_timeout=1")
	if err != nil {
		t.Fatalf("lazyPoolFor: %v", err)
	}
	entry.mu.Lock()
	entry.pool = pool
	entry.mu.Unlock()
	return pool
}

// deadPID returns a process ID that is not currently in use.
func deadPID(t *testing.T) int {
	t.Helper()
	for pid := 99999; pid > 1000; pid-- {
		if !utils.IsProcessAlive(pid) {
			return pid
		}
	}
	t.Fatal("could not find a dead PID")
	return 0
}

// postmasterPIDFile writes a postmaster.pid file at dataPath with the given
// PID and port, returning an embeddedInstance suitable for testing.
func postmasterPIDFile(t *testing.T, dataPath string, pid, port int) embeddedInstance {
	t.Helper()
	content := strconv.Itoa(pid) + "\n" + dataPath + "\n1234567890\n" + strconv.Itoa(port) + "\n/tmp\n"
	if err := os.WriteFile(filepath.Join(dataPath, "postmaster.pid"), []byte(content), 0644); err != nil {
		t.Fatalf("write postmaster.pid: %v", err)
	}
	return embeddedInstance{pid: pid, dataPath: dataPath}
}

// registeredInstance returns the embedded instance registered under key.
func registeredInstance(t *testing.T, key string) (embeddedInstance, bool) {
	t.Helper()
	embeddedPGLock.Lock()
	defer embeddedPGLock.Unlock()
	inst, ok := embeddedInstances[key]
	return inst, ok
}
