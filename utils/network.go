package utils

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

func IsFromLocalhost(req *http.Request) bool {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}

	// Trim brackets if it's an IPv6 address literal
	host = strings.Trim(host, "[]")

	return host == "127.0.0.1" || host == "::1"
}

func WriteJSONResponse(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func RequestHost(req *http.Request) string {
	forwardedHost := req.Header.Get("X-Forwarded-Host")
	if forwardedHost != "" {
		return forwardedHost
	}
	return req.Host
}

// GetFreePort asks the OS to allocate an unused port on localhost.
// Note: There is a small race window between when the port is returned
// and when it is actually used by the caller. In high-contention scenarios,
// the port may be taken by another process.
func GetFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}
