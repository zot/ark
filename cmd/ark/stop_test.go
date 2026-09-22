package main

// Tests for ark stop's portable graceful-shutdown path (windows-support Part 2).
// Test: test-StopCommand.md
// CRC: crc-CLI.md, crc-Server.md | R3441, R3442, R3443

import (
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// TestSocketReleasedWithinAliveThenReleased pins R3443: a live unix socket reads
// as not-released; a closed one reads as released.
func TestSocketReleasedWithinAliveThenReleased(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "ark.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	if socketReleasedWithin(sockPath, 200*time.Millisecond) {
		t.Fatal("socket reported released while server is up")
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if !socketReleasedWithin(sockPath, 2*time.Second) {
		t.Fatal("socket not released after the listener closed")
	}
}

// TestSocketReleasedWhenRefused pins R3443: an absent socket is released at once.
func TestSocketReleasedWhenRefused(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "ark.sock") // never bound

	start := time.Now()
	if !socketReleasedWithin(sockPath, 5*time.Second) {
		t.Fatal("a socket with no listener should read as released")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("refused dial should return promptly, took %v", elapsed)
	}
}

// TestGracefulStopReleasesSocket pins R3441/R3442/R3443: POST /shutdown over the
// socket (via the real serverClient + proxyRaw) then a dial-poll observes the
// server release the socket. The handler double responds then closes the
// listener — handleShutdown's shape minus its os.Exit, which a unit test can't
// survive.
func TestGracefulStopReleasesSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "ark.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"shutting down"}`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Tear down after the response flushes, mirroring handleShutdown.
		go ln.Close()
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	if socketReleasedWithin(sockPath, 200*time.Millisecond) {
		t.Fatal("socket reported released while the server is up")
	}

	client := serverClient(dir)
	if client == nil {
		t.Fatal("serverClient returned nil for a live socket")
	}
	_, _ = proxyRaw(client, "POST", "/shutdown", nil) // 200 or dropped conn both = initiated

	if !socketReleasedWithin(sockPath, 2*time.Second) {
		t.Fatal("socket not released after graceful stop")
	}
}
