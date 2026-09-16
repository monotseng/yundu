package clamav

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestScanStreamsClamdProtocol(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	go func() {
		conn, _ := listener.Accept()
		defer conn.Close()
		command := make([]byte, len("zINSTREAM\x00"))
		_, _ = io.ReadFull(conn, command)
		var body strings.Builder
		for {
			var size [4]byte
			_, _ = io.ReadFull(conn, size[:])
			n := binary.BigEndian.Uint32(size[:])
			if n == 0 {
				break
			}
			chunk := make([]byte, n)
			_, _ = io.ReadFull(conn, chunk)
			body.Write(chunk)
		}
		received <- body.String()
		_, _ = conn.Write([]byte("stream: OK\x00"))
	}()
	result, err := Scan(context.Background(), listener.Addr().String(), time.Second, strings.NewReader("hello scanner"))
	if err != nil || result.Status != "PASSED" {
		t.Fatalf("%+v %v", result, err)
	}
	if got := <-received; got != "hello scanner" {
		t.Fatalf("got %q", got)
	}
}
func TestScanDoesNotTreatFoundAsPassed(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()
	go func() {
		conn, _ := listener.Accept()
		defer conn.Close()
		buffer := make([]byte, 1024)
		_, _ = conn.Read(buffer)
		_, _ = conn.Write([]byte("stream: Eicar-Test-Signature FOUND\x00"))
	}()
	result, _ := Scan(context.Background(), listener.Addr().String(), time.Second, strings.NewReader("x"))
	if result.Status == "PASSED" {
		t.Fatal("infected response passed")
	}
}
