package redis

import (
	"io"
	"net"
	"strings"
	"sync/atomic"
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

func TestStoreReusesConnectionAcrossCommands(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var accepts atomic.Int32
	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		accepts.Add(1)
		defer conn.Close()
		buf := make([]byte, 512)
		var got strings.Builder
		pongs := 0
		for pongs < 2 {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			got.Write(buf[:n])
			for strings.Count(got.String(), "PING") > pongs {
				_, _ = conn.Write([]byte("+PONG\r\n"))
				pongs++
			}
		}
		close(done)
	}()

	store := NewStore(ln.Addr().String(), "", "0")
	if err := store.Ping(); err != nil {
		t.Fatal(err)
	}
	if err := store.Ping(); err != nil {
		t.Fatal(err)
	}
	<-done
	if accepts.Load() != 1 {
		t.Fatalf("expected 1 dial for 2 commands, got %d", accepts.Load())
	}
}

func TestStoreRetriesOnceAfterStaleConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		// First connection dies without a reply, like a Redis restart leaving a
		// stale socket in the pool.
		first, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 64)
		_, _ = first.Read(buf)
		_ = first.Close()

		// The retry must dial a fresh connection and get a real PONG.
		second, err := ln.Accept()
		if err != nil {
			return
		}
		defer second.Close()
		_, _ = second.Read(buf)
		_, _ = second.Write([]byte("+PONG\r\n"))
	}()

	store := NewStore(ln.Addr().String(), "", "0")
	if err := store.Ping(); err != nil {
		t.Fatalf("expected retry to recover from stale connection: %v", err)
	}
}
