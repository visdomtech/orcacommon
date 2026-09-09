//go:build !windows

package utils

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// IsDataPathInitialized checks if a PostgreSQL data directory has been initialized
// by verifying the existence of the PG_VERSION file created by initdb.
// Returns true if PG_VERSION exists, false if it does not exist.
// Returns an error for I/O or permission issues (distinct from "not initialized").
func IsDataPathInitialized(dataPath string) (bool, error) {
	pgVersionPath := filepath.Join(dataPath, "PG_VERSION")
	_, err := os.Stat(pgVersionPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// CheckPIDFile reads the postmaster.pid file in the given data directory and
// determines whether the PostgreSQL process is still running.
// Returns:
//   - exists: true if the postmaster.pid file exists (even if unreadable)
//   - alive: true if the process referenced by the PID is currently running
//   - pid: the process ID from the file, or 0 if the file doesn't exist or is invalid
//   - err: non-nil for unexpected I/O errors (permission denied, etc.)
//
// Note: os.FindProcess always succeeds on Unix; the Signal(0) probe is the
// actual liveness check. On Windows, this file is excluded via build constraint.
func CheckPIDFile(dataPath string) (exists bool, alive bool, pid int, err error) {
	pidPath := filepath.Join(dataPath, "postmaster.pid")
	file, err := os.Open(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, 0, nil
		}
		return true, false, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		pidStr := strings.TrimSpace(scanner.Text())
		parsedPID, parseErr := strconv.Atoi(pidStr)
		if parseErr == nil {
			process, findErr := os.FindProcess(parsedPID)
			// os.FindProcess always succeeds on Unix; check findErr for portability
			if findErr == nil {
				// Signal 0 tests process existence without killing it (POSIX)
				if sigErr := process.Signal(syscall.Signal(0)); sigErr == nil {
					return true, true, parsedPID, nil
				}
			}
			return true, false, parsedPID, nil
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return true, false, 0, fmt.Errorf("read postmaster.pid: %w", scanErr)
	}
	return true, false, 0, nil
}

// IsPortListening checks if a TCP port is accepting connections on the given host.
// Returns true if a connection can be established within the specified timeout.
func IsPortListening(host string, port int, timeout time.Duration) bool {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ReadPostmasterPort reads the TCP port number from line 4 of the postmaster.pid
// file in the given data directory. The postmaster.pid format is:
//
//	Line 1: PID
//	Line 2: data directory path
//	Line 3: timestamp
//	Line 4: port number
//	Line 5: Unix socket directory
func ReadPostmasterPort(dataPath string) (int, error) {
	pidPath := filepath.Join(dataPath, "postmaster.pid")
	file, err := os.Open(pidPath)
	if err != nil {
		return 0, fmt.Errorf("open postmaster.pid: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		if line == 4 {
			port, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
			if err != nil {
				return 0, fmt.Errorf("parse port from postmaster.pid line 4: %w", err)
			}
			return port, nil
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return 0, fmt.Errorf("read postmaster.pid: %w", scanErr)
	}
	return 0, fmt.Errorf("postmaster.pid has fewer than 4 lines")
}

// IsEmbeddedPGRunning is a composite check that determines whether an embedded
// PostgreSQL instance is already running at the given data directory and port.
// It delegates to ReuseEmbeddedPG which checks PID file liveness and port.
// Returns true only if the process is alive AND the port is accepting connections.
func IsEmbeddedPGRunning(dataPath string) bool {
	running, _ := ReuseEmbeddedPG(dataPath)
	return running
}

// ReuseEmbeddedPG checks whether an embedded PostgreSQL instance is already
// running at the given data directory. If running, it returns (true, port)
// where port is read from the postmaster.pid file. Returns (false, 0) if
// no running instance is detected or the data path is empty.
func ReuseEmbeddedPG(dataPath string) (running bool, port int) {
	if dataPath == "" {
		return false, 0
	}

	pid, existingPort, err := parsePostmasterInfo(dataPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("error reading postmaster.pid", "dataPath", dataPath, "error", err)
		}
		return false, 0
	}

	// os.FindProcess always succeeds on Unix; Signal(0) is the real liveness check.
	process, findErr := os.FindProcess(pid)
	if findErr != nil {
		return false, 0
	}
	if sigErr := process.Signal(syscall.Signal(0)); sigErr != nil {
		return false, 0
	}

	if !IsPortListening("127.0.0.1", existingPort, 1*time.Second) {
		slog.Warn("PID alive but port not responding", "dataPath", dataPath, "port", existingPort)
		return false, 0
	}

	slog.Info("detected reusable embedded Postgres", "dataPath", dataPath, "port", existingPort)
	return true, existingPort
}

// ErrNotPostgresProcess is returned by KillEmbeddedPG when the target PID
// does not appear to be a Postgres process. Callers can use errors.Is to
// distinguish "skipped because not postgres" from other kill failures.
var ErrNotPostgresProcess = errors.New("pid is not a postgres process")

// KillEmbeddedPG sends SIGKILL to the process identified by pid and waits for
// it to terminate. This is a last-resort fallback used when pg_ctl stop fails
// or times out during graceful shutdown. Before sending the signal, it
// verifies the target process is a Postgres process to avoid killing an
// unrelated process that may have reused the PID.
//
// Returns ErrNotPostgresProcess when the target PID is dead or does not appear
// to be a Postgres process (callers should treat this as "nothing to kill").
// Use errors.Is to distinguish from other kill failures.
func KillEmbeddedPG(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	// Verify the target process is actually a Postgres process before
	// sending SIGKILL. This guards against PID reuse after the original
	// process has exited.
	if !IsPostgresProcess(pid) {
		slog.Warn("pid does not appear to be a Postgres process, skipping kill", "pid", pid)
		return ErrNotPostgresProcess
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := process.Signal(syscall.SIGKILL); err != nil {
		// Process may have already exited — treat "no such process" and
		// "process already finished" as success.
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return fmt.Errorf("send SIGKILL to pid %d: %w", pid, err)
	}
	// Poll until the process is reaped or we time out (~5s total).
	// Exponential backoff: starts at 25ms, caps at 500ms per iteration.
	delay := 25 * time.Millisecond
	for i := 0; i < 20; i++ {
		if !IsProcessAlive(pid) {
			return nil
		}
		// Attempt non-blocking waitpid: if we are the parent, this reaps
		// the zombie and the next Signal(0) will return ESRCH.
		// When we are NOT the parent (e.g., process re-parented to init),
		// Wait4 returns ECHILD and wpid == 0 — the loop relies on
		// IsProcessAlive instead.
		if wpid, _ := syscall.Wait4(pid, nil, syscall.WNOHANG, nil); wpid == pid {
			return nil
		}
		time.Sleep(delay)
		if delay < 500*time.Millisecond {
			delay *= 2
		}
	}
	return fmt.Errorf("pid %d still alive after SIGKILL", pid)
}

// IsPostgresProcess checks whether the process identified by pid is a
// Postgres process by inspecting its command name. This guards against
// PID reuse when force-killing embedded Postgres instances.
// Exported so that the postgres package can apply the same guard before
// sending SIGTERM via sendPostgresSIGTERM.
func IsPostgresProcess(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		// Process may have exited between CheckPIDFile and here — treat as
		// not a postgres process (nothing to kill).
		return false
	}
	// Use filepath.Base to normalize macOS full-path output (e.g.,
	// /usr/local/bin/postgres → postgres) and avoid matching utility
	// processes like pg_ctl, pg_dump, pg_restore.
	comm := filepath.Base(strings.TrimSpace(string(out)))
	lower := strings.ToLower(comm)
	return lower == "postgres" || lower == "postmaster"
}

// IsProcessAlive reports whether a process with the given pid exists and is
// reachable via Signal(0). On Unix, os.FindProcess always succeeds, so the
// Signal(0) probe is the real liveness check.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// parsePostmasterInfo reads the PID (line 1) and port (line 4) from the
// postmaster.pid file in a single file open, avoiding TOCTOU races.
func parsePostmasterInfo(dataPath string) (pid int, port int, err error) {
	pidPath := filepath.Join(dataPath, "postmaster.pid")
	file, err := os.Open(pidPath)
	if err != nil {
		return 0, 0, fmt.Errorf("open postmaster.pid: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		switch line {
		case 1:
			pid, err = strconv.Atoi(text)
			if err != nil {
				return 0, 0, fmt.Errorf("parse PID from postmaster.pid line 1: %w", err)
			}
		case 4:
			port, err = strconv.Atoi(text)
			if err != nil {
				return 0, 0, fmt.Errorf("parse port from postmaster.pid line 4: %w", err)
			}
			return pid, port, nil
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return 0, 0, fmt.Errorf("read postmaster.pid: %w", scanErr)
	}
	return 0, 0, fmt.Errorf("postmaster.pid has fewer than 4 lines")
}
