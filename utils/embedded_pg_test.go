//go:build !windows

package utils

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestIsDataPathInitialized(t *testing.T) {
	t.Run("returns false when directory does not exist", func(t *testing.T) {
		result, err := IsDataPathInitialized("/nonexistent/path/that/does/not/exist")
		if err != nil {
			t.Fatalf("IsDataPathInitialized() unexpected error: %v", err)
		}
		if result {
			t.Error("IsDataPathInitialized() = true for nonexistent path, want false")
		}
	})

	t.Run("returns false when PG_VERSION does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		result, err := IsDataPathInitialized(tmpDir)
		if err != nil {
			t.Fatalf("IsDataPathInitialized() unexpected error: %v", err)
		}
		if result {
			t.Error("IsDataPathInitialized() = true when PG_VERSION missing, want false")
		}
	})

	t.Run("returns true when PG_VERSION exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		pgVersionPath := filepath.Join(tmpDir, "PG_VERSION")
		if err := os.WriteFile(pgVersionPath, []byte("15\n"), 0644); err != nil {
			t.Fatalf("Failed to create PG_VERSION: %v", err)
		}

		result, err := IsDataPathInitialized(tmpDir)
		if err != nil {
			t.Fatalf("IsDataPathInitialized() unexpected error: %v", err)
		}
		if !result {
			t.Error("IsDataPathInitialized() = false when PG_VERSION exists, want true")
		}
	})
}

func TestCheckPIDFile(t *testing.T) {
	t.Run("returns false when directory does not exist", func(t *testing.T) {
		exists, alive, pid, err := CheckPIDFile("/nonexistent/path")
		if err != nil {
			t.Fatalf("CheckPIDFile() unexpected error: %v", err)
		}
		if exists {
			t.Error("CheckPIDFile() exists = true for nonexistent path, want false")
		}
		if alive {
			t.Error("CheckPIDFile() alive = true for nonexistent path, want false")
		}
		if pid != 0 {
			t.Errorf("CheckPIDFile() pid = %d for nonexistent path, want 0", pid)
		}
	})

	t.Run("returns false when postmaster.pid does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		exists, alive, pid, err := CheckPIDFile(tmpDir)
		if err != nil {
			t.Fatalf("CheckPIDFile() unexpected error: %v", err)
		}
		if exists {
			t.Error("CheckPIDFile() exists = true when no pid file, want false")
		}
		if alive {
			t.Error("CheckPIDFile() alive = true when no pid file, want false")
		}
		if pid != 0 {
			t.Errorf("CheckPIDFile() pid = %d when no pid file, want 0", pid)
		}
	})

	t.Run("returns stale when PID is invalid", func(t *testing.T) {
		tmpDir := t.TempDir()
		pidPath := filepath.Join(tmpDir, "postmaster.pid")
		if err := os.WriteFile(pidPath, []byte("not_a_number\n"), 0644); err != nil {
			t.Fatalf("Failed to create postmaster.pid: %v", err)
		}

		exists, alive, pid, err := CheckPIDFile(tmpDir)
		if err != nil {
			t.Fatalf("CheckPIDFile() unexpected error: %v", err)
		}
		if !exists {
			t.Error("CheckPIDFile() exists = false when pid file exists, want true")
		}
		if alive {
			t.Error("CheckPIDFile() alive = true for invalid PID, want false")
		}
		if pid != 0 {
			t.Errorf("CheckPIDFile() pid = %d for invalid PID, want 0", pid)
		}
	})

	t.Run("returns stale when process is not running", func(t *testing.T) {
		tmpDir := t.TempDir()
		pidPath := filepath.Join(tmpDir, "postmaster.pid")

		// Spawn a child process and immediately kill it to get a guaranteed-dead PID
		cmd := exec.Command("sleep", "60")
		if err := cmd.Start(); err != nil {
			t.Fatalf("Failed to start child process: %v", err)
		}
		deadPID := cmd.Process.Pid
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()

		if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", deadPID)), 0644); err != nil {
			t.Fatalf("Failed to create postmaster.pid: %v", err)
		}

		exists, alive, pid, err := CheckPIDFile(tmpDir)
		if err != nil {
			t.Fatalf("CheckPIDFile() unexpected error: %v", err)
		}
		if !exists {
			t.Error("CheckPIDFile() exists = false when pid file exists, want true")
		}
		if alive {
			t.Error("CheckPIDFile() alive = true for non-running process, want false")
		}
		if pid != deadPID {
			t.Errorf("CheckPIDFile() pid = %d, want %d", pid, deadPID)
		}
	})

	t.Run("returns alive when process is running", func(t *testing.T) {
		tmpDir := t.TempDir()
		pidPath := filepath.Join(tmpDir, "postmaster.pid")
		currentPID := os.Getpid()
		if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", currentPID)), 0644); err != nil {
			t.Fatalf("Failed to create postmaster.pid: %v", err)
		}

		exists, alive, pid, err := CheckPIDFile(tmpDir)
		if err != nil {
			t.Fatalf("CheckPIDFile() unexpected error: %v", err)
		}
		if !exists {
			t.Error("CheckPIDFile() exists = false when pid file exists, want true")
		}
		if !alive {
			t.Error("CheckPIDFile() alive = false for running process, want true")
		}
		if pid != currentPID {
			t.Errorf("CheckPIDFile() pid = %d, want %d", pid, currentPID)
		}
	})

	t.Run("returns exists but not alive when PID file is empty", func(t *testing.T) {
		tmpDir := t.TempDir()
		pidPath := filepath.Join(tmpDir, "postmaster.pid")
		if err := os.WriteFile(pidPath, []byte(""), 0644); err != nil {
			t.Fatalf("Failed to create empty postmaster.pid: %v", err)
		}

		exists, alive, pid, err := CheckPIDFile(tmpDir)
		if err != nil {
			t.Fatalf("CheckPIDFile() unexpected error: %v", err)
		}
		if !exists {
			t.Error("CheckPIDFile() exists = false for empty pid file, want true")
		}
		if alive {
			t.Error("CheckPIDFile() alive = true for empty pid file, want false")
		}
		if pid != 0 {
			t.Errorf("CheckPIDFile() pid = %d for empty pid file, want 0", pid)
		}
	})
}

func TestIsPortListening(t *testing.T) {
	t.Run("returns false when no listener", func(t *testing.T) {
		port, err := GetFreePort()
		if err != nil {
			t.Fatalf("GetFreePort() error = %v", err)
		}

		result := IsPortListening("127.0.0.1", port, 100*time.Millisecond)
		if result {
			t.Errorf("IsPortListening() = true for unused port %d, want false", port)
		}
	})

	t.Run("returns true when listener exists", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Failed to create listener: %v", err)
		}
		defer listener.Close()

		addr := listener.Addr().(*net.TCPAddr)
		result := IsPortListening("127.0.0.1", addr.Port, 1*time.Second)
		if !result {
			t.Errorf("IsPortListening() = false for listening port %d, want true", addr.Port)
		}
	})

	t.Run("returns false for unreachable address", func(t *testing.T) {
		// Use TEST-NET-1 (RFC 5737) - unroutable IP that fails at TCP level, not DNS
		result := IsPortListening("192.0.2.1", 5432, 100*time.Millisecond)
		if result {
			t.Error("IsPortListening() = true for unreachable address, want false")
		}
	})
}

func TestReadPostmasterPort(t *testing.T) {
	t.Run("reads port from line 4", func(t *testing.T) {
		tmpDir := t.TempDir()
		pidPath := filepath.Join(tmpDir, "postmaster.pid")
		// Realistic postmaster.pid content: PID, datadir, timestamp, port, socket
		content := fmt.Sprintf("%d\n%s\n1691000000\n5432\n/tmp\n", os.Getpid(), tmpDir)
		if err := os.WriteFile(pidPath, []byte(content), 0644); err != nil {
			t.Fatalf("write postmaster.pid: %v", err)
		}

		port, err := ReadPostmasterPort(tmpDir)
		if err != nil {
			t.Fatalf("ReadPostmasterPort() error: %v", err)
		}
		if port != 5432 {
			t.Errorf("ReadPostmasterPort() = %d, want 5432", port)
		}
	})

	t.Run("returns error when file missing", func(t *testing.T) {
		_, err := ReadPostmasterPort("/nonexistent/path")
		if err == nil {
			t.Error("ReadPostmasterPort() expected error for missing file")
		}
	})

	t.Run("returns error when file has fewer than 4 lines", func(t *testing.T) {
		tmpDir := t.TempDir()
		pidPath := filepath.Join(tmpDir, "postmaster.pid")
		if err := os.WriteFile(pidPath, []byte("12345\n/data\n1691000000\n"), 0644); err != nil {
			t.Fatalf("write postmaster.pid: %v", err)
		}

		_, err := ReadPostmasterPort(tmpDir)
		if err == nil {
			t.Error("ReadPostmasterPort() expected error for short file")
		}
	})

	t.Run("returns error when port is not a number", func(t *testing.T) {
		tmpDir := t.TempDir()
		pidPath := filepath.Join(tmpDir, "postmaster.pid")
		content := "12345\n/data\n1691000000\nnot_a_port\n/tmp\n"
		if err := os.WriteFile(pidPath, []byte(content), 0644); err != nil {
			t.Fatalf("write postmaster.pid: %v", err)
		}

		_, err := ReadPostmasterPort(tmpDir)
		if err == nil {
			t.Error("ReadPostmasterPort() expected error for invalid port")
		}
	})
}

func TestIsEmbeddedPGRunning(t *testing.T) {
	t.Run("returns false when data path not initialized", func(t *testing.T) {
		tmpDir := t.TempDir()
		result := IsEmbeddedPGRunning(tmpDir)
		if result {
			t.Error("IsEmbeddedPGRunning() = true for uninitialized data path, want false")
		}
	})

	t.Run("returns false when initialized but no running process", func(t *testing.T) {
		tmpDir := t.TempDir()
		pgVersionPath := filepath.Join(tmpDir, "PG_VERSION")
		if err := os.WriteFile(pgVersionPath, []byte("15\n"), 0644); err != nil {
			t.Fatalf("Failed to create PG_VERSION: %v", err)
		}

		result := IsEmbeddedPGRunning(tmpDir)
		if result {
			t.Error("IsEmbeddedPGRunning() = true when no PID file, want false")
		}
	})
}

func TestReuseEmbeddedPG(t *testing.T) {
	t.Run("returns false for empty dataPath", func(t *testing.T) {
		running, port := ReuseEmbeddedPG("")
		if running || port != 0 {
			t.Errorf("ReuseEmbeddedPG(\"\") = (%v, %d), want (false, 0)", running, port)
		}
	})

	t.Run("returns false when no PID file", func(t *testing.T) {
		tmpDir := t.TempDir()
		running, port := ReuseEmbeddedPG(tmpDir)
		if running || port != 0 {
			t.Errorf("ReuseEmbeddedPG() = (%v, %d), want (false, 0)", running, port)
		}
	})

	t.Run("returns true and port when PG is running", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Start a TCP listener to simulate a running PG port.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Failed to create listener: %v", err)
		}
		defer listener.Close()
		listenPort := listener.Addr().(*net.TCPAddr).Port

		// Write a valid postmaster.pid with current PID and the listening port.
		pidPath := filepath.Join(tmpDir, "postmaster.pid")
		content := fmt.Sprintf("%d\n%s\n1691000000\n%d\n/tmp\n", os.Getpid(), tmpDir, listenPort)
		if err := os.WriteFile(pidPath, []byte(content), 0644); err != nil {
			t.Fatalf("write postmaster.pid: %v", err)
		}

		running, port := ReuseEmbeddedPG(tmpDir)
		if !running {
			t.Error("ReuseEmbeddedPG() = false, want true for running PG")
		}
		if port != listenPort {
			t.Errorf("ReuseEmbeddedPG() port = %d, want %d", port, listenPort)
		}
	})

	t.Run("returns false when PID alive but port not listening", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Write postmaster.pid with current PID but a port nobody is listening on.
		freePort, err := GetFreePort()
		if err != nil {
			t.Fatalf("GetFreePort: %v", err)
		}
		pidPath := filepath.Join(tmpDir, "postmaster.pid")
		content := fmt.Sprintf("%d\n%s\n1691000000\n%d\n/tmp\n", os.Getpid(), tmpDir, freePort)
		if err := os.WriteFile(pidPath, []byte(content), 0644); err != nil {
			t.Fatalf("write postmaster.pid: %v", err)
		}

		running, port := ReuseEmbeddedPG(tmpDir)
		if running {
			t.Errorf("ReuseEmbeddedPG() = (true, %d), want (false, 0) for dead port", port)
		}
	})
}
