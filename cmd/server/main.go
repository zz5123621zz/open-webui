package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/auth"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/httpapi"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger, os.Args[1:]); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	dataStore, err := store.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer func() { _ = dataStore.Close() }()
	if err := cleanupStaleTemporaryFiles(cfg.DataDir, time.Now().Add(-time.Hour)); err != nil {
		logger.Warn("temporary file cleanup failed", "error", err)
	}

	if len(args) > 0 && args[0] == "user" {
		return runUserCommand(dataStore, cfg.DataDir, args[1:])
	}
	if len(args) > 0 && args[0] == "healthcheck" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return dataStore.Ready(ctx)
	}
	if len(args) > 0 && args[0] == "backup" {
		return runBackupCommand(dataStore, cfg.DataDir, args[1:])
	}
	if len(args) > 0 && args[0] != "serve" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	interrupted, err := dataStore.InterruptActiveResponses(context.Background())
	if err != nil {
		return err
	}
	if interrupted > 0 {
		logger.Warn(
			"recovered responses interrupted by an earlier service stop",
			"responses", interrupted,
		)
	}

	modelClient := provider.NewClient(cfg.Provider, version)
	handler := httpapi.New(cfg, dataStore, modelClient, logger)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go maintenanceLoop(
		shutdownContext,
		dataStore,
		cfg.DataDir,
		cfg.Lifecycle.RetentionTTL,
		cfg.Lifecycle.MaintenanceInterval,
		logger,
	)
	go func() {
		<-shutdownContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		responseShutdown := make(chan struct{})
		go func() {
			if err := handler.Shutdown(ctx); err != nil {
				logger.Warn("response shutdown incomplete", "error", err)
			}
			close(responseShutdown)
		}()
		_ = server.Shutdown(ctx)
		<-responseShutdown
	}()

	logger.Info("server starting", "version", version, "addr", cfg.HTTPAddr, "data_dir", cfg.DataDir)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func cleanupStaleTemporaryFiles(dataDir string, olderThan time.Time) error {
	for _, relative := range []string{filepath.Join("tmp", "provider"), filepath.Join("tmp", "uploads")} {
		root := filepath.Join(dataDir, relative)
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(olderThan) {
				_ = os.Remove(filepath.Join(root, entry.Name()))
			}
		}
	}
	return nil
}

func maintenanceLoop(
	ctx context.Context,
	dataStore *store.Store,
	dataDir string,
	retentionTTL, interval time.Duration,
	logger *slog.Logger,
) {
	runMaintenance(ctx, dataStore, dataDir, retentionTTL, logger)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runMaintenance(ctx, dataStore, dataDir, retentionTTL, logger)
		}
	}
}

func runMaintenance(
	ctx context.Context,
	dataStore *store.Store,
	dataDir string,
	retentionTTL time.Duration,
	logger *slog.Logger,
) {
	maintenanceContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if err := dataStore.DeleteExpiredSessions(maintenanceContext); err != nil {
		logger.Warn("expired session cleanup failed", "error", err)
	}
	cutoff := time.Now().Add(-retentionTTL).UnixMilli()
	paths, conversations, err := dataStore.PurgeExpiredRetained(maintenanceContext, cutoff)
	if err != nil {
		logger.Warn("retained conversation cleanup failed", "error", err)
		return
	}
	removed, removeErr := removeStoredFiles(dataDir, paths)
	if removeErr != nil {
		logger.Warn("retained attachment cleanup incomplete", "error", removeErr)
	}
	if conversations > 0 || removed > 0 {
		logger.Info(
			"retention cleanup completed",
			"conversations", conversations,
			"attachments", removed,
		)
	}
}

func runBackupCommand(dataStore *store.Store, dataDir string, args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	output := flags.String("output", "", "destination SQLite snapshot")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*output) == "" {
		*output = filepath.Join(dataDir, "backups", "app-"+time.Now().UTC().Format("20060102T150405Z")+".db")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := dataStore.Backup(ctx, *output); err != nil {
		return err
	}
	fmt.Printf("Created database backup %s\n", *output)
	return nil
}

func runUserCommand(dataStore *store.Store, dataDir string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: server user <add|list|password|enable|disable|workbench|purge-all>")
	}
	switch args[0] {
	case "add":
		return runUserAddCommand(dataStore, args[1:])
	case "list":
		return runUserListCommand(dataStore, args[1:])
	case "password":
		return runUserPasswordCommand(dataStore, args[1:])
	case "enable", "disable":
		return runUserStatusCommand(dataStore, args[0], args[1:])
	case "workbench":
		return runUserWorkbenchCommand(dataStore, args[1:])
	case "purge-all":
		return runUserPurgeAllCommand(dataStore, dataDir, args[1:])
	default:
		return errors.New("usage: server user <add|list|password|enable|disable|workbench|purge-all>")
	}
}

func runUserAddCommand(dataStore *store.Store, args []string) error {
	flags := flag.NewFlagSet("user add", flag.ContinueOnError)
	username := flags.String("username", "", "login username")
	displayName := flags.String("display-name", "", "display name")
	role := flags.String("role", "user", "user role: user or admin")
	if err := flags.Parse(args); err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)
	*username = promptLine(reader, "Username: ", *username)
	if strings.TrimSpace(*displayName) == "" {
		fmt.Print("Display name: ")
		value, _ := reader.ReadString('\n')
		*displayName = strings.TrimSpace(value)
	}

	password, err := promptPassword()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	user, err := dataStore.CreateUserWithRole(
		context.Background(), *username, *displayName, hash, *role,
	)
	if err != nil {
		return err
	}
	fmt.Printf("Created %s %s (%s)\n", user.Role, user.Username, user.ID)
	return nil
}

func runUserListCommand(dataStore *store.Store, args []string) error {
	flags := flag.NewFlagSet("user list", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("user list does not accept positional arguments")
	}
	users, err := dataStore.ListUsers(context.Background())
	if err != nil {
		return err
	}
	if len(users) == 0 {
		fmt.Println("No users")
		return nil
	}
	for _, user := range users {
		fmt.Printf("%s\t%s\t%s\t%s\n", user.Username, user.Role, user.Status, user.DisplayName)
	}
	return nil
}

func runUserPasswordCommand(dataStore *store.Store, args []string) error {
	flags := flag.NewFlagSet("user password", flag.ContinueOnError)
	username := flags.String("username", "", "login username")
	if err := flags.Parse(args); err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	*username = promptLine(reader, "Username: ", *username)
	user, err := dataStore.UserByUsername(context.Background(), *username)
	if err != nil {
		return err
	}
	password, err := promptPassword()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := dataStore.UpdatePassword(context.Background(), user.ID, hash); err != nil {
		return err
	}
	fmt.Printf("Updated password and revoked all sessions for %s\n", user.Username)
	return nil
}

func runUserStatusCommand(dataStore *store.Store, action string, args []string) error {
	flags := flag.NewFlagSet("user "+action, flag.ContinueOnError)
	username := flags.String("username", "", "login username")
	if err := flags.Parse(args); err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	*username = promptLine(reader, "Username: ", *username)
	status := "active"
	if action == "disable" {
		status = "disabled"
	}
	if err := dataStore.SetUserStatusByUsername(context.Background(), *username, status); err != nil {
		return err
	}
	fmt.Printf("User %s is now %s\n", *username, status)
	return nil
}

func runUserWorkbenchCommand(dataStore *store.Store, args []string) error {
	flags := flag.NewFlagSet("user workbench", flag.ContinueOnError)
	username := flags.String("username", "", "login username")
	workbench := flags.String("workbench", "", "initial workbench: general or restaurant")
	if err := flags.Parse(args); err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	*username = promptLine(reader, "Username: ", *username)
	if strings.TrimSpace(*workbench) == "" {
		*workbench = promptLine(reader, "Workbench (general/restaurant): ", "")
	}
	setting, err := dataStore.SetInitialWorkbenchByUsername(
		context.Background(), *username, *workbench, "",
	)
	if err != nil {
		return err
	}
	fmt.Printf(
		"User %s initial workbench is now %s (effective: %s)\n",
		*username, setting.Initial, setting.Effective,
	)
	return nil
}

func runUserPurgeAllCommand(dataStore *store.Store, dataDir string, args []string) error {
	flags := flag.NewFlagSet("user purge-all", flag.ContinueOnError)
	confirm := flags.Bool("confirm", false, "confirm deletion of every user and workspace")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*confirm {
		return errors.New("user purge-all requires --confirm")
	}
	paths, deleted, err := dataStore.DeleteAllUsers(context.Background())
	if err != nil {
		return err
	}
	removed, removeErr := removeStoredFiles(dataDir, paths)
	if removeErr != nil {
		return fmt.Errorf(
			"deleted %d users and %d attachment records, but file cleanup was incomplete: %w",
			deleted, len(paths), removeErr,
		)
	}
	fmt.Printf("Deleted %d users and removed %d stored attachments\n", deleted, removed)
	return nil
}

func removeStoredFiles(dataDir string, paths []string) (int, error) {
	root := filepath.Clean(dataDir)
	removed := 0
	var firstErr error
	for _, storagePath := range paths {
		fullPath := filepath.Clean(filepath.Join(root, storagePath))
		relative, err := filepath.Rel(root, fullPath)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			if firstErr == nil {
				firstErr = fmt.Errorf("invalid stored path %q", storagePath)
			}
			continue
		}
		if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}

func promptLine(reader *bufio.Reader, label, current string) string {
	if strings.TrimSpace(current) != "" {
		return strings.TrimSpace(current)
	}
	fmt.Print(label)
	value, _ := reader.ReadString('\n')
	return strings.TrimSpace(value)
}

func promptPassword() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("password prompt requires a terminal")
	}
	fmt.Print("Password (minimum 6 characters): ")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	fmt.Print("Confirm password: ")
	confirmation, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	if string(password) != string(confirmation) {
		return "", errors.New("passwords do not match")
	}
	return string(password), nil
}
