package csm

import (
	"net"
	"testing"
	"time"
)

func TestWaitUDPPortFree(t *testing.T) {
	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Skipf("no UDP here: %v", err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	if err := waitUDPPortFree(port, 300*time.Millisecond); err == nil {
		t.Fatalf("port %d is held, but reported free", port)
	}
	_ = pc.Close()
	if err := waitUDPPortFree(port, time.Second); err != nil {
		t.Fatalf("port %d is free, but: %v", port, err)
	}
	if err := waitUDPPortFree(0, 0); err != nil {
		t.Fatalf("no port: %v", err)
	}
}
