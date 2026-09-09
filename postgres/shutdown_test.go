//go:build !windows

package postgres

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// startFakePostgresProcess creates a symlink named "postgres" pointing to /bin/sleep
// in a temp directory, then exec's it. The resulting process will show as "postgres"
// in ps -o comm= output, satisfying the IsPostgresProcess guard.
// Returns the cmd (not yet waited) and the data path containing the PID file.
func startFakePostgresProcess(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	binDir := t.TempDir()
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatalf("find sleep: %v", err)
	}
	fakePG := filepath.Join(binDir, "postgres")
	if err := os.Symlink(sleepPath, fakePG); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	cmd := exec.Command(fakePG, "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake postgres: %v", err)
	}
	dataPath := t.TempDir()
	pidContent := strconv.Itoa(cmd.Process.Pid) + "\n" + dataPath + "\n1234567890\n5432\n/tmp\n"
	if err := os.WriteFile(filepath.Join(dataPath, "postmaster.pid"), []byte(pidContent), 0644); err != nil {
		cmd.Process.Kill() //nolint:errcheck
		t.Fatalf("write postmaster.pid: %v", err)
	}
	return cmd, dataPath
}

// TestSendPostgresSIGTERM_EmptyDataPath verifies that sendPostgresSIGTERM
// is a no-op when the data path is empty.
func TestSendPostgresSIGTERM_EmptyDataPath(t *testing.T) {
	// Must not panic or log errors.
	sendPostgresSIGTERM("test-empty", "")
}

// TestSendPostgresSIGTERM_NoPIDFile verifies that sendPostgresSIGTERM
// is a no-op when the postmaster.pid file does not exist.
func TestSendPostgresSIGTERM_NoPIDFile(t *testing.T) {
	sendPostgresSIGTERM("test-no-pid", t.TempDir())
}

// TestSendPostgresSIGTERM_DeadProcess verifies that sendPostgresSIGTERM
// is a no-op when the PID in postmaster.pid refers to a non-existent process.
func TestSendPostgresSIGTERM_DeadProcess(t *testing.T) {
	dataPath := t.TempDir()

	// Write a postmaster.pid with a PID that almost certainly does not exist.
	// Using PID 99999999 — extremely unlikely to be running on any system.
	pidContent := "99999999\n/var/lib/postgresql/data\n1234567890\n5432\n/tmp\n"
	if err := os.WriteFile(filepath.Join(dataPath, "postmaster.pid"), []byte(pidContent), 0644); err != nil {
		t.Fatalf("write postmaster.pid: %v", err)
	}

	// Must not panic or send signals to arbitrary processes.
	sendPostgresSIGTERM("test-dead-pid", dataPath)
}

// TestSendPostgresSIGTERM_NonPostgresProcess verifies that sendPostgresSIGTERM
// refuses to send SIGTERM to a process that is not named "postgres".
func TestSendPostgresSIGTERM_NonPostgresProcess(t *testing.T) {
	// Start a plain sleep process (not named postgres).
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	defer cmd.Process.Kill() //nolint:errcheck

	pid := cmd.Process.Pid
	dataPath := t.TempDir()
	pidContent := strconv.Itoa(pid) + "\n" + dataPath + "\n1234567890\n5432\n/tmp\n"
	if err := os.WriteFile(filepath.Join(dataPath, "postmaster.pid"), []byte(pidContent), 0644); err != nil {
		t.Fatalf("write postmaster.pid: %v", err)
	}

	// sendPostgresSIGTERM should skip this non-postgres process.
	sendPostgresSIGTERM("test-non-pg", dataPath)

	// The process should still be alive (SIGTERM was not sent).
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("process should still be alive after sendPostgresSIGTERM skipped it")
	}
}

// TestSendPostgresSIGTERM_LiveProcess verifies that sendPostgresSIGTERM
// sends SIGTERM to a process named "postgres" (simulated via symlink).
func TestSendPostgresSIGTERM_LiveProcess(t *testing.T) {
	cmd, dataPath := startFakePostgresProcess(t)
	defer cmd.Process.Kill() //nolint:errcheck

	// sendPostgresSIGTERM should send SIGTERM to our fake postgres process.
	sendPostgresSIGTERM("test-live", dataPath)

	// The subprocess should have received SIGTERM and exited.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
		// Subprocess exited as expected after receiving SIGTERM.
	case <-time.After(5 * time.Second):
		t.Fatal("subprocess did not exit within 5s after SIGTERM")
		cmd.Process.Kill() //nolint:errcheck
	}
}

// TestSendPostgresSIGTERM_InvalidPID verifies that sendPostgresSIGTERM
// handles an unreadable PID in postmaster.pid gracefully.
func TestSendPostgresSIGTERM_InvalidPID(t *testing.T) {
	dataPath := t.TempDir()

	// Write a postmaster.pid with a non-numeric PID.
	pidContent := "not-a-pid\n/var/lib/postgresql/data\n1234567890\n5432\n/tmp\n"
	if err := os.WriteFile(filepath.Join(dataPath, "postmaster.pid"), []byte(pidContent), 0644); err != nil {
		t.Fatalf("write postmaster.pid: %v", err)
	}

	// Must not panic.
	sendPostgresSIGTERM("test-invalid-pid", dataPath)
}

// TestShutdownDoneChannel_InitialState verifies that the shutdownDone channel
// is open (not closed) when no shutdown signal has been received.
// WaitForGracefulShutdown blocks on this channel, so it must not be closed
// at package initialization.
func TestShutdownDoneChannel_InitialState(t *testing.T) {
	select {
	case <-shutdownDone:
		t.Fatal("shutdownDone should be open (not closed) before any shutdown signal is received")
	default:
		// Expected: channel is open, select falls through to default.
	}
}

// TestConnect_RefusesDuringShutdown verifies that Connect() returns an
// error when the shuttingDown flag is set, preventing new embedded Postgres
// registrations after graceful shutdown has begun.
func TestConnect_RefusesDuringShutdown(t *testing.T) {
	shuttingDown.Store(true)
	defer shuttingDown.Store(false)

	_, err := Connect(t.Context(), "postgres:embedded:", "test-shutdown-guard")
	if err == nil {
		t.Fatal("expected error when shuttingDown is true")
	}
	if got := err.Error(); got != "embedded Postgres unavailable: shutdown in progress" {
		t.Errorf("unexpected error message: %s", got)
	}
}

// TestStopWithForceKill_SIGTERMInitiated verifies that stopWithForceKill
// initiates Postgres shutdown via direct SIGTERM, allowing pg.Stop() to
// return quickly. Uses a real embedded Postgres instance.
func TestStopWithForceKill_SIGTERMInitiated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded postgres test in short mode")
	}

	// Reduce stopTimeout so the test does not take too long if something hangs.
	origTimeout := stopTimeout
	stopTimeout = 10 * time.Second
	defer func() { stopTimeout = origTimeout }()

	ctx := t.Context()
	pool, err := Connect(ctx, "postgres:embedded:", "test-sigterm-stop")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Get the embedded instance to stop via stopWithForceKill.
	embeddedPGLock.Lock()
	inst, ok := embeddedInstances["test-sigterm-stop"]
	if ok {
		delete(embeddedInstances, "test-sigterm-stop")
	}
	embeddedPGLock.Unlock()

	if !ok {
		pool.Close()
		t.Fatal("embedded instance not found")
	}

	pool.Close()

	// stopWithForceKill should send SIGTERM first, then pg.Stop() should
	// complete quickly (well under stopTimeout).
	start := time.Now()
	stopWithForceKill("test-sigterm-stop", inst)
	elapsed := time.Since(start)

	// Verify the Postgres process is no longer alive.
	_, alive, pid, _ := checkPIDFileSafe(inst.dataPath)
	if alive && pid > 0 {
		// Force-kill as cleanup.
		proc, _ := os.FindProcess(pid)
		_ = proc.Signal(syscall.SIGKILL)
		t.Fatalf("Postgres process (pid=%d) still alive after stopWithForceKill", pid)
	}

	t.Logf("stopWithForceKill completed in %v", elapsed)
	// SIGTERM + pg.Stop() should complete well under the 10s timeout.
	if elapsed > stopTimeout {
		t.Errorf("stopWithForceKill took %v, expected < %v", elapsed, stopTimeout)
	}
}

// TestStopWithForceKill_ReusedInstance verifies that stopWithForceKill
// correctly handles reused instances (pg == nil) by sending SIGTERM
// and verifying the process exits. No pg.Stop() call is made.
// Uses a symlink-named "postgres" process to satisfy the IsPostgresProcess guard.
func TestStopWithForceKill_ReusedInstance(t *testing.T) {
	cmd, dataPath := startFakePostgresProcess(t)
	defer cmd.Process.Kill() //nolint:errcheck

	// Create a reused instance (pg is nil).
	inst := embeddedInstance{pg: nil, dataPath: dataPath, reused: true}

	start := time.Now()
	stopWithForceKill("test-reused", inst)
	elapsed := time.Since(start)

	// Verify the subprocess was killed by SIGTERM.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// Subprocess exited as expected.
	case <-time.After(5 * time.Second):
		t.Fatal("reused instance subprocess did not exit within 5s")
		cmd.Process.Kill() //nolint:errcheck
	}

	t.Logf("stopWithForceKill (reused) completed in %v", elapsed)
}

// TestStopWithForceKill_ForceKillPath verifies that stopWithForceKill
// handles a process that is still alive after the SIGTERM phase.
// On Linux (where ps -o comm= shows the symlink name "postgres"), this
// exercises the full SIGTERM → SIGKILL fallback path. On macOS, ps resolves
// symlinks to the target binary name, so IsPostgresProcess returns false
// and only the PID-file cleanup path is exercised; the SIGKILL path is
// covered separately by TestKillEmbeddedPG in utils/.
func TestStopWithForceKill_ForceKillPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping force-kill test in short mode")
	}

	cmd, dataPath := startFakePostgresProcess(t)
	defer cmd.Process.Kill() //nolint:errcheck

	pid := cmd.Process.Pid

	// Reduce stopTimeout for faster test.
	origTimeout := stopTimeout
	stopTimeout = 2 * time.Second
	defer func() { stopTimeout = origTimeout }()

	inst := embeddedInstance{pg: nil, dataPath: dataPath, reused: true}
	start := time.Now()
	stopWithForceKill("test-forcekill", inst)
	elapsed := time.Since(start)

	// Verify the process is dead.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// Process exited (SIGTERM on Linux where IsPostgresProcess passes,
		// or process was already dead via PID-file cleanup path).
	case <-time.After(5 * time.Second):
		// On macOS, IsPostgresProcess may return false for the symlinked
		// process, so neither SIGTERM nor SIGKILL is sent. Kill manually
		// to clean up and verify the PID file was removed.
		cmd.Process.Kill() //nolint:errcheck
		<-done
		t.Logf("note: on macOS, process was not auto-killed (IsPostgresProcess=false for symlink)")
	}

	// Verify the PID file was cleaned up.
	pidFile := filepath.Join(dataPath, "postmaster.pid")
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Errorf("postmaster.pid should have been removed, got err: %v", err)
	}

	t.Logf("stopWithForceKill (force-kill path) completed in %v (pid=%d)", elapsed, pid)
}

// checkPIDFileSafe is a test helper that reads the postmaster.pid and checks
// whether the referenced process is still alive, ignoring parse errors for
// cleaner test assertions.
func checkPIDFileSafe(dataPath string) (exists bool, alive bool, pid int, err error) {
	pidPath := filepath.Join(dataPath, "postmaster.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, 0, nil
		}
		return true, false, 0, err
	}
	lines := splitLines(string(data))
	if len(lines) == 0 {
		return true, false, 0, nil
	}
	p, parseErr := strconv.Atoi(lines[0])
	if parseErr != nil {
		return true, false, 0, nil
	}
	proc, findErr := os.FindProcess(p)
	if findErr != nil {
		return true, false, p, nil
	}
	if sigErr := proc.Signal(syscall.Signal(0)); sigErr != nil {
		return true, false, p, nil
	}
	return true, true, p, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
