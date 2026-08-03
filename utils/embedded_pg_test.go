package utils

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsDataPathInitialized(t *testing.T) {
	t.Run("returns false when directory does not exist", func(t *testing.T) {
		result := IsDataPathInitialized("/nonexistent/path/that/does/not/exist")
		if result {
			t.Error("IsDataPathInitialized() = true for nonexistent path, want false")
		}
	})

	t.Run("returns false when PG_VERSION does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		result := IsDataPathInitialized(tmpDir)
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

		result := IsDataPathInitialized(tmpDir)
		if !result {
			t.Error("IsDataPathInitialized() = false when PG_VERSION exists, want true")
		}
	})
}

func TestCheckPIDFile(t *testing.T) {
	t.Run("returns false when directory does not exist", func(t *testing.T) {
		exists, alive, pid := CheckPIDFile("/nonexistent/path")
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
		exists, alive, pid := CheckPIDFile(tmpDir)
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

		exists, alive, pid := CheckPIDFile(tmpDir)
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
		// Use a very high PID that is unlikely to be running
		if err := os.WriteFile(pidPath, []byte("999999999\n"), 0644); err != nil {
			t.Fatalf("Failed to create postmaster.pid: %v", err)
		}

		exists, alive, pid := CheckPIDFile(tmpDir)
		if !exists {
			t.Error("CheckPIDFile() exists = false when pid file exists, want true")
		}
		if alive {
			t.Error("CheckPIDFile() alive = true for non-running process, want false")
		}
		if pid != 999999999 {
			t.Errorf("CheckPIDFile() pid = %d, want 999999999", pid)
		}
	})

	t.Run("returns alive when process is running", func(t *testing.T) {
		tmpDir := t.TempDir()
		pidPath := filepath.Join(tmpDir, "postmaster.pid")
		// Use current process PID which is definitely running
		currentPID := os.Getpid()
		if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", currentPID)), 0644); err != nil {
			t.Fatalf("Failed to create postmaster.pid: %v", err)
		}

		exists, alive, pid := CheckPIDFile(tmpDir)
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
}

func TestIsPortListening(t *testing.T) {
	t.Run("returns false when no listener", func(t *testing.T) {
		// Get a free port that is not being used
		port, err := GetFreePort()
		if err != nil {
			t.Fatalf("GetFreePort() error = %v", err)
		}

		result := IsPortListening("127.0.0.1", uint32(port), 100*time.Millisecond)
		if result {
			t.Errorf("IsPortListening() = true for unused port %d, want false", port)
		}
	})

	t.Run("returns true when listener exists", func(t *testing.T) {
		// Start a listener
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Failed to create listener: %v", err)
		}
		defer listener.Close()

		addr := listener.Addr().(*net.TCPAddr)
		result := IsPortListening("127.0.0.1", uint32(addr.Port), 1*time.Second)
		if !result {
			t.Errorf("IsPortListening() = false for listening port %d, want true", addr.Port)
		}
	})

	t.Run("returns false for invalid host", func(t *testing.T) {
		result := IsPortListening("invalid.host.that.does.not.exist", 5432, 100*time.Millisecond)
		if result {
			t.Error("IsPortListening() = true for invalid host, want false")
		}
	})
}
