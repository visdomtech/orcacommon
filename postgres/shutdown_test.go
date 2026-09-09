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

// TestSendPostgresSIGTERM_LiveProcess verifies that sendPostgresSIGTERM
// sends SIGTERM to the process identified by the PID in postmaster.pid.
// It spawns a real subprocess as the target to validate signal delivery.
func TestSendPostgresSIGTERM_LiveProcess(t *testing.T) {
	// Start a long-running subprocess that we can signal.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	defer cmd.Process.Kill() //nolint:errcheck // cleanup best-effort

	pid := cmd.Process.Pid
	dataPath := t.TempDir()

	// Write a fake postmaster.pid with the subprocess PID.
	pidContent := strconv.Itoa(pid) + "\n" + dataPath + "\n1234567890\n5432\n/tmp\n"
	if err := os.WriteFile(filepath.Join(dataPath, "postmaster.pid"), []byte(pidContent), 0644); err != nil {
		t.Fatalf("write postmaster.pid: %v", err)
	}

	// sendPostgresSIGTERM should send SIGTERM to our subprocess.
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
