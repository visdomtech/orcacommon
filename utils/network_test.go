package utils

import (
	"testing"
)

func TestGetFreePort(t *testing.T) {
	port, err := GetFreePort()
	if err != nil {
		t.Fatalf("GetFreePort() error = %v", err)
	}

	// Port should be in valid range (1-65535)
	if port < 1 || port > 65535 {
		t.Errorf("GetFreePort() returned invalid port %d, want 1-65535", port)
	}

	// Get another free port - should be different (high probability)
	port2, err := GetFreePort()
	if err != nil {
		t.Fatalf("GetFreePort() second call error = %v", err)
	}

	// Two consecutive calls should typically return different ports
	// (not guaranteed, but very likely with ephemeral port allocation)
	if port == port2 {
		t.Logf("Warning: Two consecutive GetFreePort() calls returned same port %d", port)
	}
}
