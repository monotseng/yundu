//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func startDaemon(args []string, logPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	absLog, err := filepath.Abs(logPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absLog), 0755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(absLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
	cmd := exec.Command(exe, withoutArg(args, "--start")...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func processRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

func withoutArg(args []string, target string) []string {
	result := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != target {
			result = append(result, arg)
		}
	}
	return result
}
