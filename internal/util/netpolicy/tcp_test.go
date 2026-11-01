package netpolicy

import (
	"context"
	"net"
	"reflect"
	"testing"
)

func TestListenTCPDisablesMPTCPDespiteEnvironment(t *testing.T) {
	t.Setenv("GODEBUG", "multipathtcp=1")
	listener, err := ListenTCP(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("ordinary TCP client cannot connect: %v", err)
	}
	defer conn.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	enabled, err := accepted.(*net.TCPConn).MultipathTCP()
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("panel listener must not use MPTCP, even with GODEBUG=multipathtcp=1")
	}
}

func TestChildEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env, want []string
	}{
		{"empty", nil, []string{"GODEBUG=multipathtcp=0"}},
		{"preserve settings", []string{"PATH=/bin", "GODEBUG=asyncpreemptoff=1,multipathtcp=1,http2client=0", "XRAY_LOCATION_ASSET=/data"}, []string{"PATH=/bin", "XRAY_LOCATION_ASSET=/data", "GODEBUG=asyncpreemptoff=1,http2client=0,multipathtcp=0"}},
		{"duplicate entries", []string{"GODEBUG=multipathtcp=2", "GODEBUG=gctrace=1,multipathtcp=3,"}, []string{"GODEBUG=gctrace=1,multipathtcp=0"}},
		{"similar key", []string{"GODEBUG_EXTRA=keep", "GODEBUG="}, []string{"GODEBUG_EXTRA=keep", "GODEBUG=multipathtcp=0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]string(nil), tc.env...)
			if got := ChildEnvironment(tc.env); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(tc.env, original) {
				t.Fatal("caller environment was mutated")
			}
		})
	}
}
