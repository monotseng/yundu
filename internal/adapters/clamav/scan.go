package clamav

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

type Result struct {
	Status             string `json:"status"`
	EngineVersion      string `json:"engine_version,omitempty"`
	DefinitionsVersion string `json:"definitions_version,omitempty"`
	Detail             string `json:"detail,omitempty"`
}

func Scan(ctx context.Context, address string, timeout time.Duration, r io.Reader) (Result, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return Result{Status: "ERROR"}, err
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)
	if _, err = conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return Result{Status: "ERROR"}, err
	}
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := r.Read(buffer)
		if n > 0 {
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], uint32(n))
			if _, err = conn.Write(size[:]); err != nil {
				return Result{Status: "ERROR"}, err
			}
			if _, err = conn.Write(buffer[:n]); err != nil {
				return Result{Status: "ERROR"}, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Result{Status: "ERROR"}, readErr
		}
	}
	if _, err = conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return Result{Status: "ERROR"}, err
	}
	reply, err := bufio.NewReader(conn).ReadString(0)
	if err != nil && !errors.Is(err, io.EOF) {
		return Result{Status: "ERROR"}, err
	}
	reply = strings.TrimSpace(strings.TrimSuffix(reply, "\x00"))
	if strings.HasSuffix(reply, "OK") {
		return Result{Status: "PASSED"}, nil
	}
	if strings.Contains(reply, "FOUND") {
		return Result{Status: "INFECTED", Detail: "malware signature detected"}, nil
	}
	return Result{Status: "ERROR"}, errors.New("clamd returned an indeterminate result")
}
