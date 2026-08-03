//go:build windows

package postgres

// reuseEmbeddedPG is a no-op on Windows. The embedded postgres PID/port
// detection utilities rely on POSIX signals and are not available on Windows.
func reuseEmbeddedPG(dataPath string) (running bool, port int) {
	return false, 0
}
