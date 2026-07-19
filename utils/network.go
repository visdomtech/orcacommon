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
