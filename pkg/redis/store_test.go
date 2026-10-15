package redis

import (
	"io"
	"net"
	"strings"
	"testing"
)

func TestStoreSendsAuthBeforeCommand(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		var got strings.Builder
		for !strings.Contains(got.String(), "PING") {
			n, err := conn.Read(buf)
			if err != nil && err != io.EOF {
				return
			}
			got.Write(buf[:n])
			if strings.Contains(got.String(), "AUTH") && !strings.Contains(got.String(), "+OK-sent") {
				_, _ = conn.Write([]byte("+OK\r\n"))
				got.WriteString("+OK-sent")
			}
		}
		_, _ = conn.Write([]byte("+PONG\r\n"))
		received <- got.String()
	}()

	if err := NewStore(ln.Addr().String(), "s3cret", "0").Ping(); err != nil {
		t.Fatal(err)
	}
	got := <-received
	if strings.Index(got, "AUTH") > strings.Index(got, "PING") || !strings.Contains(got, "s3cret") {
		t.Fatalf("AUTH must come first with the password, got %q", got)
	}
}
