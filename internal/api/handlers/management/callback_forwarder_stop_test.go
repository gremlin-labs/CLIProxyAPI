package management

import (
	"errors"
	"net"
	"strconv"
	"testing"
	"time"
)

// A browser often pre-opens a connection it never uses. Graceful shutdown waits on
// such a connection, so stopping must force-close it and free the port promptly.
func TestStopCallbackForwarderClosesIdlePreopenedConnection(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	forwarder, err := startCallbackForwarder(port, "test", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("startCallbackForwarder() error = %v", err)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("pre-open connection: %v", err)
	}
	defer func() { _ = conn.Close() }()

	stopCallbackForwarderInstance(port, forwarder)

	// The pre-opened connection must have been closed by the server.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, errRead := conn.Read(make([]byte, 1))
	var netErr net.Error
	if errRead == nil || (errors.As(errRead, &netErr) && netErr.Timeout()) {
		t.Fatalf("pre-opened connection is still open after stop (read error: %v)", errRead)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %d not released after stop: %v", port, err)
	}
	_ = listener.Close()
}
