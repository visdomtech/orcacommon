//go:build windows

package utils

import "time"

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
	return 0, nil
}

// IsEmbeddedPGRunning is a no-op stub on Windows.
func IsEmbeddedPGRunning(dataPath string) bool {
	return false
}

// ReuseEmbeddedPG is a no-op stub on Windows.
func ReuseEmbeddedPG(dataPath string) (running bool, port int) {
	return false, 0
}
