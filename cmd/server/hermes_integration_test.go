package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestHermesTokenCLIEndToEnd(t *testing.T) {
	ctx := context.Background()
	dataStore := openCommandTestStore(t)
	user, err := dataStore.CreateUser(ctx, "father", "Father", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx,
		user.Username,
		guidance.WorkbenchRestaurant,
		"",
	); err != nil {
		t.Fatal(err)
	}

	var issueOutput bytes.Buffer
	if err := runIntegrationCommand(
		dataStore,
		[]string{
			"hermes-token",
			"issue",
			"--username", user.Username,
			"--label", "father-restaurant",
			"--model", "gpt-5.6-sol",
			"--reasoning-effort", "high",
		},
		&issueOutput,
	); err != nil {
		t.Fatal(err)
	}
	tokenPattern := regexp.MustCompile(`hbr_[A-Za-z0-9_-]{43}`)
	rawToken := tokenPattern.FindString(issueOutput.String())
	if rawToken == "" ||
		!strings.Contains(issueOutput.String(), "shown once") {
		t.Fatalf("issue output = %q", issueOutput.String())
	}
	credentials, err := dataStore.ListHermesRestaurantCredentials(ctx)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials = %#v, error = %v", credentials, err)
	}

	var listOutput bytes.Buffer
	if err := runIntegrationCommand(
		dataStore,
		[]string{"hermes-token", "list"},
		&listOutput,
	); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		credentials[0].ID,
		user.Username,
		"father-restaurant",
		"gpt-5.6-sol",
		"high",
		"active",
	} {
		if !strings.Contains(listOutput.String(), expected) {
			t.Errorf("list output lacks %q: %q", expected, listOutput.String())
		}
	}
	if strings.Contains(listOutput.String(), rawToken) {
		t.Fatal("list output disclosed the raw token")
	}

	var revokeOutput bytes.Buffer
	if err := runIntegrationCommand(
		dataStore,
		[]string{
			"hermes-token",
			"revoke",
			"--id", credentials[0].ID,
		},
		&revokeOutput,
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(revokeOutput.String(), credentials[0].ID) {
		t.Fatalf("revoke output = %q", revokeOutput.String())
	}
	if _, err := dataStore.AuthenticateHermesRestaurantCredential(
		ctx,
		rawToken,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked token authentication error = %v", err)
	}
}

func TestHermesTokenCLIRejectsIncompleteCommands(t *testing.T) {
	dataStore := openCommandTestStore(t)
	for _, args := range [][]string{
		nil,
		{"other"},
		{"hermes-token"},
		{"hermes-token", "other"},
		{"hermes-token", "issue", "--username", "missing"},
		{"hermes-token", "revoke"},
		{"hermes-token", "list", "unexpected"},
	} {
		if err := runIntegrationCommand(
			dataStore,
			args,
			io.Discard,
		); err == nil {
			t.Fatalf("runIntegrationCommand(%#v) succeeded", args)
		}
	}
}

func TestMaintenancePurgesExpiredHermesAudioRecordAndFile(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	dataStore, err := store.Open(ctx, filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	user, err := dataStore.CreateUser(ctx, "cleanup-user", "Cleanup", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx,
		user.Username,
		guidance.WorkbenchRestaurant,
		"",
	); err != nil {
		t.Fatal(err)
	}
	credential, _, err := dataStore.CreateHermesRestaurantCredential(
		ctx,
		user.Username,
		"cleanup",
		"gpt-5.6-sol",
		"high",
	)
	if err != nil {
		t.Fatal(err)
	}
	audioBytes := bytes.Repeat([]byte{1}, 100)
	digest := sha256.Sum256(audioBytes)
	storagePath := filepath.Join(
		"hermes-restaurant-audio",
		"expired.wav",
	)
	fullPath := filepath.Join(dataDir, storagePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, audioBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	audio := store.HermesRestaurantAudio{
		ID: "expired-audio", CredentialID: credential.ID,
		UserID: user.ID, RequestKey: "expired-request",
		PartIndex: 0, FileName: "answer-01-of-01.wav",
		StoragePath: storagePath, ByteSize: int64(len(audioBytes)),
		SHA256:    hex.EncodeToString(digest[:]),
		CreatedAt: now - 20_000, ExpiresAt: now - 10_000,
	}
	if err := dataStore.CreateHermesRestaurantAudio(ctx, audio); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runMaintenance(ctx, dataStore, dataDir, 30*24*time.Hour, logger)

	if _, err := os.Stat(fullPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired file stat error = %v", err)
	}
	if _, err := dataStore.HermesRestaurantAudioByID(
		ctx,
		credential.ID,
		user.ID,
		audio.ID,
		now-15_000,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired record lookup error = %v", err)
	}
}

func openCommandTestStore(t *testing.T) *store.Store {
	t.Helper()
	dataStore, err := store.Open(
		context.Background(),
		filepath.Join(t.TempDir(), "app.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	return dataStore
}
