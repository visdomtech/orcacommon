//go:build !windows

package postgres

import (
	"log/slog"
	"time"

	"github.com/visdomtech/orcacommon/utils"
)

// reuseEmbeddedPG checks whether an embedded PostgreSQL instance is already
// running at the given data directory. If running, it returns (true, port)
// where port is read from the postmaster.pid file. Returns (false, 0) if
// no running instance is detected or the data path is empty.
func reuseEmbeddedPG(dataPath string) (running bool, port int) {
	if dataPath == "" {
		return false, 0
	}

	_, alive, _, err := utils.CheckPIDFile(dataPath)
	if err != nil {
		slog.Warn("error checking PID file", "dataPath", dataPath, "error", err)
		return false, 0
	}
	if !alive {
		return false, 0
	}

	existingPort, err := utils.ReadPostmasterPort(dataPath)
	if err != nil {
		slog.Warn("error reading port from postmaster.pid", "dataPath", dataPath, "error", err)
		return false, 0
	}

	if !utils.IsPortListening("127.0.0.1", existingPort, 1*time.Second) {
		slog.Warn("PID alive but port not responding", "dataPath", dataPath, "port", existingPort)
		return false, 0
	}

	return true, existingPort
}
