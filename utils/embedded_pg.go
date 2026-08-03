package utils

import (
	"bufio"
	"fmt"
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
func IsDataPathInitialized(dataPath string) bool {
	pgVersionPath := filepath.Join(dataPath, "PG_VERSION")
	_, err := os.Stat(pgVersionPath)
	return err == nil
}

// CheckPIDFile reads the postmaster.pid file in the given data directory and
// determines whether the PostgreSQL process is still running.
// Returns:
//   - exists: true if the postmaster.pid file exists
//   - alive: true if the process referenced by the PID is currently running
//   - pid: the process ID from the file, or 0 if the file doesn't exist or is invalid
func CheckPIDFile(dataPath string) (exists bool, alive bool, pid int) {
	pidPath := filepath.Join(dataPath, "postmaster.pid")
	file, err := os.Open(pidPath)
	if err != nil {
		return false, false, 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		pidStr := strings.TrimSpace(scanner.Text())
		parsedPID, err := strconv.Atoi(pidStr)
		if err == nil {
			process, err := os.FindProcess(parsedPID)
			if err == nil {
				// Signal 0 tests process existence without killing it (POSIX)
				if err := process.Signal(syscall.Signal(0)); err == nil {
					return true, true, parsedPID
				}
			}
			return true, false, parsedPID
		}
	}
	return true, false, 0
}

// IsPortListening checks if a TCP port is accepting connections on the given host.
// Returns true if a connection can be established within the specified timeout.
func IsPortListening(host string, port uint32, timeout time.Duration) bool {
	address := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
