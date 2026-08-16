package reconcile_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestTCPHealthChecker_ReachableAddress_ReturnsNil(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	checker := reconcile.TCPHealthChecker{}
	err = checker.Check(context.Background(), resource.HealthCheckContract{
		Kind:   "tcp",
		Config: map[string]string{"address": ln.Addr().String()},
	})
	if err != nil {
		t.Errorf("Check(reachable) = %v, want nil", err)
	}
}

func TestTCPHealthChecker_UnreachableAddress_ReturnsError(t *testing.T) {
	// Bind and immediately close: the port is very likely free again but
	// nothing is listening, giving a fast, reliable connection-refused
	// rather than a real DNS/network-dependent unreachable host.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	checker := reconcile.TCPHealthChecker{}
	err = checker.Check(context.Background(), resource.HealthCheckContract{
		Kind:   "tcp",
		Config: map[string]string{"address": addr},
	})
	if err == nil {
		t.Fatal("Check(unreachable) = nil, want an error")
	}
	if errors.Is(err, reconcile.ErrNotConfigured) {
		t.Errorf("Check(unreachable) = %v, want a real connection error, not ErrNotConfigured", err)
	}
}

func TestTCPHealthChecker_Kind(t *testing.T) {
	tests := []struct {
		name string
		kind string
	}{
		{"empty kind", ""},
		{"unrecognized kind", "http"},
	}
	checker := reconcile.TCPHealthChecker{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checker.Check(context.Background(), resource.HealthCheckContract{Kind: tt.kind})
			if !errors.Is(err, reconcile.ErrNotConfigured) {
				t.Errorf("Check(Kind=%q) = %v, want ErrNotConfigured", tt.kind, err)
			}
		})
	}
}

func TestTCPHealthChecker_MissingAddress_ReturnsRealError(t *testing.T) {
	checker := reconcile.TCPHealthChecker{}
	err := checker.Check(context.Background(), resource.HealthCheckContract{Kind: "tcp"})
	if err == nil {
		t.Fatal("Check(no address) = nil, want an error")
	}
	if errors.Is(err, reconcile.ErrNotConfigured) {
		t.Error("Check(Kind=tcp, no address) should be a real config error, not ErrNotConfigured -- the operator did configure a check, just wrongly")
	}
}
