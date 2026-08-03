//go:build !windows

package utils

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
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
