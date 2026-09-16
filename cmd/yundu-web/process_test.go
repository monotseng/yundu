package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPreparePIDFileRejectsRunningProcess(t *testing.T) {
	name := filepath.Join(t.TempDir(), "yundu.pid")
	if err := os.WriteFile(name, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := preparePIDFile(name); err == nil {
		t.Fatal("expected duplicate start rejection")
	}
}

func TestPreparePIDFileRemovesStaleContent(t *testing.T) {
	name := filepath.Join(t.TempDir(), "yundu.pid")
	if err := os.WriteFile(name, []byte("not-a-pid\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := preparePIDFile(name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("stale pid remains: %v", err)
	}
}

func TestWritePIDFileDoesNotOverwrite(t *testing.T) {
	name := filepath.Join(t.TempDir(), "yundu.pid")
	if err := os.WriteFile(name, []byte("123\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writePIDFile(name); err == nil {
		t.Fatal("expected exclusive create failure")
	}
}

func TestWithoutArg(t *testing.T) {
	got := withoutArg([]string{"--start", "--config", "x.yaml"}, "--start")
	if len(got) != 2 || got[0] != "--config" || got[1] != "x.yaml" {
		t.Fatalf("unexpected args: %#v", got)
	}
}
