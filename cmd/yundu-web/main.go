package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"yundu/internal/config"
	"yundu/internal/httpapi"
	"yundu/internal/identity"
	store "yundu/internal/persistence/mysql"
	"yundu/internal/recovery"
	"yundu/internal/secrets"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "configs/config.local.yaml", "YAML configuration file")
	pidFile := flag.String("pid-file", "yundu.pid", "service PID file")
	logFile := flag.String("log-file", "yundu.log", "daemon log file")
	start := flag.Bool("start", false, "start service in background")
	stopServiceFlag := flag.Bool("stop", false, "stop background service")
	status := flag.Bool("status", false, "show service status")
	migrate := flag.Bool("migrate", false, "apply database migrations and exit")
	bootstrapAdmin := flag.String("bootstrap-admin", "", "create the first administrator username")
	displayName := flag.String("display-name", "", "display name for --bootstrap-admin")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	recoveryReason := flag.String("enter-recovery-mode", "", "freeze release/download and revoke credentials after database restore; value is the reason")
	showVersion := flag.Bool("version", false, "show version")
	flag.Parse()
	if *showVersion {
		fmt.Printf("云渡文件交换平台 %s\n", version)
		return
	}
	if countTrue(*start, *stopServiceFlag, *status, *migrate, *bootstrapAdmin != "", *recoveryReason != "") > 1 {
		slog.Error("service management and maintenance operations are mutually exclusive")
		os.Exit(2)
	}
	if *stopServiceFlag {
		if err := stopService(*pidFile, 30*time.Second); err != nil {
			slog.Error("stop service", "error", err)
			os.Exit(1)
		}
		slog.Info("service stopped", "pid_file", *pidFile)
		return
	}
	if *status {
		pid, running, err := serviceStatus(*pidFile)
		if err != nil {
			slog.Error("read service status", "error", err)
			os.Exit(1)
		}
		if !running {
			fmt.Println("云渡服务未运行")
			os.Exit(3)
		}
		fmt.Printf("云渡服务正在运行（PID %d）\n", pid)
		return
	}
	if *start {
		if err := preparePIDFile(*pidFile); err != nil {
			slog.Error("start service", "error", err)
			os.Exit(1)
		}
		if err := startDaemon(os.Args[1:], *logFile); err != nil {
			slog.Error("start service", "error", err)
			os.Exit(1)
		}
		pid, err := waitForPID(*pidFile, 10*time.Second)
		if err != nil {
			slog.Error("service did not become ready", "error", err, "log_file", *logFile)
			os.Exit(1)
		}
		slog.Info("service started", "pid", pid, "pid_file", *pidFile, "log_file", *logFile)
		return
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("configuration rejected", "error", err)
		os.Exit(2)
	}
	if *checkConfig {
		slog.Info("configuration valid", "environment", cfg.Environment)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, cfg.Database)
	if err != nil {
		slog.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	// Keep the single-binary lifecycle safe: every service start brings the
	// database schema up to the version embedded in that binary before serving.
	if err := store.Migrate(ctx, db); err != nil {
		slog.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	if *migrate {
		slog.Info("database migrations applied")
		return
	}
	if *recoveryReason != "" {
		if err := recovery.New(db).Enter(ctx, *recoveryReason); err != nil {
			slog.Error("enter recovery mode failed", "error", err)
			os.Exit(1)
		}
		slog.Info("recovery mode entered; sessions, challenges and grants revoked")
		return
	}
	if *bootstrapAdmin != "" {
		box, err := secrets.NewBox(cfg.Security.MasterKey)
		if err != nil {
			slog.Error("bootstrap requires master key", "error", err)
			os.Exit(1)
		}
		token, expires, err := identity.NewService(db, box).BootstrapAdmin(ctx, *bootstrapAdmin, *displayName)
		if err != nil {
			slog.Error("bootstrap administrator failed", "error", err)
			os.Exit(1)
		}
		fmt.Printf("首次管理员激活凭据（仅显示一次）：%s\n有效期至：%s\n", token, expires.Format(time.RFC3339))
		return
	}
	api, err := httpapi.New(cfg, db, version)
	if err != nil {
		slog.Error("initialize server", "error", err)
		os.Exit(1)
	}
	api.StartWorkers(ctx)
	if err := writePIDFile(*pidFile); err != nil {
		slog.Error("write pid file", "error", err)
		os.Exit(1)
	}
	defer removeOwnPIDFile(*pidFile)
	srv := &http.Server{Addr: cfg.Server.ListenAddress, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 75 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		slog.Info("yundu started", "address", cfg.Server.ListenAddress, "version", version)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server stopped", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	api.SetReady(false)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown incomplete", "error", err)
		os.Exit(1)
	}
	slog.Info("yundu stopped")
}

func countTrue(values ...bool) int {
	n := 0
	for _, value := range values {
		if value {
			n++
		}
	}
	return n
}

func writePIDFile(name string) error {
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		os.Remove(name)
		return err
	}
	return f.Close()
}

func readPID(name string) (int, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		return 0, fmt.Errorf("invalid pid file %s", name)
	}
	return pid, nil
}

func preparePIDFile(name string) error {
	pid, err := readPID(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil && processRunning(pid) {
		return fmt.Errorf("service is already running (pid %d)", pid)
	}
	if removeErr := os.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

func serviceStatus(name string) (int, bool, error) {
	pid, err := readPID(name)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return pid, processRunning(pid), nil
}

func stopService(name string, timeout time.Duration) error {
	pid, err := readPID(name)
	if err != nil {
		return err
	}
	if !processRunning(pid) {
		_ = os.Remove(name)
		return fmt.Errorf("stale pid file removed; process %d was not running", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processRunning(pid) {
			_ = os.Remove(name)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for process %d to stop", pid)
}

func waitForPID(name string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pid, err := readPID(name)
		if err == nil && processRunning(pid) {
			return pid, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0, fmt.Errorf("pid file %s was not created", name)
}

func removeOwnPIDFile(name string) {
	pid, err := readPID(name)
	if err == nil && pid == os.Getpid() {
		_ = os.Remove(name)
	}
}
