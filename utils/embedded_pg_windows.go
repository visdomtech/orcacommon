//go:build windows

package utils

import (
	"errors"
	"fmt"
	"time"
)

// ErrNotPostgresProcess is returned when KillEmbeddedPG determines the target
// PID does not belong to a Postgres process. On Windows this is a stub.
var ErrNotPostgresProcess = errors.New("pid is not a postgres process")

// IsPostgresProcess is a no-op stub on Windows.
func IsPostgresProcess(_ int) bool {
	return false
}

// IsDataPathInitialized is a no-op stub on Windows.
// The embedded postgres utilities rely on POSIX signals and are not available on Windows.
func IsDataPathInitialized(dataPath string) (bool, error) {
	return false, nil
}

// CheckPIDFile is a no-op stub on Windows.
func CheckPIDFile(dataPath string) (exists bool, alive bool, pid int, err error) {
	return false, false, 0, nil
}

// IsPortListening is a no-op stub on Windows.
func IsPortListening(host string, port int, timeout time.Duration) bool {
	return false
}

// ReadPostmasterPort is a no-op stub on Windows.
func ReadPostmasterPort(dataPath string) (int, error) {
	return 0, fmt.Errorf("ReadPostmasterPort: not supported on windows")
}

// IsEmbeddedPGRunning is a no-op stub on Windows.
func IsEmbeddedPGRunning(dataPath string) bool {
	return false
}

// ReuseEmbeddedPG is a no-op stub on Windows.
func ReuseEmbeddedPG(dataPath string) (running bool, port int) {
	return false, 0
}

// KillEmbeddedPG is a no-op stub on Windows.
// Force-kill via SIGKILL is not applicable on Windows; the graceful pg.Stop()
// (pg_ctl stop) is the only shutdown path on this platform.
func KillEmbeddedPG(pid int) error {
	return fmt.Errorf("KillEmbeddedPG: not supported on windows")
}

// IsProcessAlive is a no-op stub on Windows.
func IsProcessAlive(pid int) bool {
	return false
}
