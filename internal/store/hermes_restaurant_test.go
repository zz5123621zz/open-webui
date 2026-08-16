package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
)

func TestHermesRestaurantCredentialLifecycleAndTokenHashing(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user := createHermesRestaurantUser(t, dataStore, "bridge-user")

	credential, rawToken, err := dataStore.CreateHermesRestaurantCredential(
		ctx,
		user.Username,
		"father-restaurant",
		"gpt-5.6-sol",
		"high",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rawToken, "hbr_") || len(rawToken) != 47 {
		t.Fatalf("raw token shape = %q", rawToken)
	}
	time.Sleep(2 * time.Millisecond)
	second, secondToken, err := dataStore.CreateHermesRestaurantCredential(
		ctx,
		user.Username,
		"father-restaurant-secondary",
		"gpt-5.6-sol",
		"xhigh",
	)
	if err != nil {
		t.Fatal(err)
	}
	if rawToken == secondToken || credential.ID == second.ID {
		t.Fatal("credential token or ID was reused")
	}

	var storedHash []byte
	if err := dataStore.db.QueryRowContext(
		ctx,
		`SELECT token_hash FROM hermes_restaurant_credentials WHERE id = ?`,
		credential.ID,
	).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	expectedHash := sha256.Sum256([]byte(rawToken))
	if !bytes.Equal(storedHash, expectedHash[:]) {
		t.Fatalf("stored token hash = %x, want %x", storedHash, expectedHash)
	}
	if bytes.Contains(storedHash, []byte(rawToken)) {
		t.Fatal("raw token was stored in token_hash")
	}

	authenticated, err := dataStore.AuthenticateHermesRestaurantCredential(
		ctx,
		rawToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.ID != credential.ID ||
		authenticated.UserID != user.ID ||
		authenticated.LastUsedAt == 0 {
		t.Fatalf("authenticated credential = %#v", authenticated)
	}
	if _, err := dataStore.AuthenticateHermesRestaurantCredential(
		ctx,
		"hbr_"+strings.Repeat("A", 43),
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong token error = %v", err)
	}

	credentials, err := dataStore.ListHermesRestaurantCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 2 ||
		credentials[0].ID != second.ID ||
		credentials[1].ID != credential.ID {
		t.Fatalf("credentials = %#v", credentials)
	}
	if err := dataStore.RevokeHermesRestaurantCredential(ctx, credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.AuthenticateHermesRestaurantCredential(
		ctx,
		rawToken,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token authentication error = %v", err)
	}
	if err := dataStore.RevokeHermesRestaurantCredential(
		ctx,
		credential.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke error = %v", err)
	}
}

func TestHermesRestaurantCredentialRequiresActiveRestaurantUser(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	general, err := dataStore.CreateUser(ctx, "general-user", "General", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := dataStore.CreateHermesRestaurantCredential(
		ctx,
		general.Username,
		"invalid",
		"gpt-5.6-sol",
		"high",
	); err == nil {
		t.Fatal("issued credential for general-workbench user")
	}

	restaurant := createHermesRestaurantUser(t, dataStore, "restaurant-user")
	credential, token, err := dataStore.CreateHermesRestaurantCredential(
		ctx,
		restaurant.Username,
		"restaurant",
		"gpt-5.6-sol",
		"high",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetWorkbenchPreference(
		ctx,
		restaurant.ID,
		guidance.WorkbenchGeneral,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.AuthenticateHermesRestaurantCredential(
		ctx,
		token,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("general-workbench authentication error = %v", err)
	}
	if _, err := dataStore.SetWorkbenchPreference(
		ctx,
		restaurant.ID,
		guidance.WorkbenchRestaurant,
	); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SetUserStatusByUsername(
		ctx,
		restaurant.Username,
		"disabled",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.AuthenticateHermesRestaurantCredential(
		ctx,
		token,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled-user authentication error = %v", err)
	}
	if credential.UserID != restaurant.ID {
		t.Fatalf("credential user = %q, want %q", credential.UserID, restaurant.ID)
	}
}

func TestHermesRestaurantCredentialRejectsUnsafeMetadata(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user := createHermesRestaurantUser(t, dataStore, "metadata-user")
	for _, test := range []struct {
		label  string
		model  string
		effort string
	}{
		{"line\nbreak", "gpt-5.6-sol", "high"},
		{"label", "model\tinjection", "high"},
		{"label", "gpt-5.6-sol", "impossible"},
		{"", "gpt-5.6-sol", "high"},
	} {
		if _, _, err := dataStore.CreateHermesRestaurantCredential(
			ctx,
			user.Username,
			test.label,
			test.model,
			test.effort,
		); err == nil {
			t.Fatalf("unsafe credential metadata accepted: %#v", test)
		}
	}
}

func TestHermesRestaurantSessionMappingIsStableAndIsolated(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user := createHermesRestaurantUser(t, dataStore, "session-user")
	firstCredential, _, err := dataStore.CreateHermesRestaurantCredential(
		ctx, user.Username, "first", "gpt-5.6-sol", "high",
	)
	if err != nil {
		t.Fatal(err)
	}
	secondCredential, _, err := dataStore.CreateHermesRestaurantCredential(
		ctx, user.Username, "second", "gpt-5.6-sol", "high",
	)
	if err != nil {
		t.Fatal(err)
	}

	first, err := dataStore.HermesRestaurantConversation(
		ctx, firstCredential, "hermes-session-a", "First", 30,
	)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := dataStore.HermesRestaurantConversation(
		ctx, firstCredential, "hermes-session-a", "Ignored", 30,
	)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.ID != first.ID {
		t.Fatalf("repeated session conversation = %q, want %q", repeated.ID, first.ID)
	}
	differentSession, err := dataStore.HermesRestaurantConversation(
		ctx, firstCredential, "hermes-session-b", "Second", 30,
	)
	if err != nil {
		t.Fatal(err)
	}
	differentCredential, err := dataStore.HermesRestaurantConversation(
		ctx, secondCredential, "hermes-session-a", "Third", 30,
	)
	if err != nil {
		t.Fatal(err)
	}
	if differentSession.ID == first.ID ||
		differentCredential.ID == first.ID ||
		differentCredential.ID == differentSession.ID {
		t.Fatalf(
			"session mappings not isolated: %q %q %q",
			first.ID,
			differentSession.ID,
			differentCredential.ID,
		)
	}

	var storedHash []byte
	if err := dataStore.db.QueryRowContext(ctx, `
		SELECT external_session_hash
		FROM hermes_restaurant_sessions
		WHERE credential_id = ? AND conversation_id = ?
	`, firstCredential.ID, first.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte("hermes-session-a"))
	if !bytes.Equal(storedHash, expected[:]) ||
		bytes.Equal(storedHash, []byte("hermes-session-a")) {
		t.Fatalf("stored external session hash = %x", storedHash)
	}
}

func TestHermesResponseLookupIsIdempotentAndUserScoped(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user := createHermesRestaurantUser(t, dataStore, "request-user")
	other := createHermesRestaurantUser(t, dataStore, "request-other")
	conversation, err := dataStore.CreateConversation(
		ctx,
		user.ID,
		"Request",
		"gpt-5.6-sol",
		"high",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, assistant, err := dataStore.BeginResponse(
		ctx,
		user.ID,
		conversation.ID,
		"hbr:credential:request",
		"设计菜品",
		"gpt-5.6-sol",
		"high",
		"high",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := dataStore.CompleteAssistant(
		ctx,
		user.ID,
		assistant.ID,
		AssistantResult{
			Status: "completed",
			Parts: []NewMessagePart{
				{Type: "text", TextContent: "完整答案"},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := dataStore.HermesResponseByClientRequestID(
		ctx,
		user.ID,
		"hbr:credential:request",
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ID != completed.ID ||
		recovered.Status != "completed" ||
		len(recovered.Parts) != 1 ||
		recovered.Parts[0].TextContent != "完整答案" {
		t.Fatalf("recovered response = %#v", recovered)
	}
	if _, err := dataStore.HermesResponseByClientRequestID(
		ctx,
		other.ID,
		"hbr:credential:request",
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user response lookup error = %v", err)
	}
}

func TestHermesRestaurantAudioOwnershipExpiryAndCleanup(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	firstUser := createHermesRestaurantUser(t, dataStore, "audio-user-a")
	secondUser := createHermesRestaurantUser(t, dataStore, "audio-user-b")
	firstCredential, _, err := dataStore.CreateHermesRestaurantCredential(
		ctx, firstUser.Username, "audio-a", "gpt-5.6-sol", "high",
	)
	if err != nil {
		t.Fatal(err)
	}
	secondCredential, _, err := dataStore.CreateHermesRestaurantCredential(
		ctx, secondUser.Username, "audio-b", "gpt-5.6-sol", "high",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	digest := sha256.Sum256([]byte("wav"))
	first := HermesRestaurantAudio{
		ID: "audio-one", CredentialID: firstCredential.ID,
		UserID: firstUser.ID, RequestKey: "request-one",
		PartIndex: 0, FileName: "answer-01-of-02.wav",
		StoragePath: "hermes-restaurant-audio/audio-one.wav",
		ByteSize:    100, SHA256: hex.EncodeToString(digest[:]),
		CreatedAt: now, ExpiresAt: now + 10_000,
	}
	second := first
	second.ID = "audio-two"
	second.PartIndex = 1
	second.FileName = "answer-02-of-02.wav"
	second.StoragePath = "hermes-restaurant-audio/audio-two.wav"
	if err := dataStore.CreateHermesRestaurantAudioBatch(
		ctx,
		[]HermesRestaurantAudio{second, first},
	); err != nil {
		t.Fatal(err)
	}
	records, err := dataStore.HermesRestaurantAudioForRequest(
		ctx,
		firstCredential.ID,
		firstUser.ID,
		"request-one",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 ||
		records[0].ID != first.ID ||
		records[1].ID != second.ID {
		t.Fatalf("ordered audio records = %#v", records)
	}
	if _, err := dataStore.HermesRestaurantAudioByID(
		ctx,
		secondCredential.ID,
		secondUser.ID,
		first.ID,
		now,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-credential audio lookup error = %v", err)
	}
	if _, err := dataStore.HermesRestaurantAudioByID(
		ctx,
		firstCredential.ID,
		firstUser.ID,
		first.ID,
		first.ExpiresAt,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired audio lookup error = %v", err)
	}

	mismatched := first
	mismatched.ID = "audio-mismatch"
	mismatched.UserID = secondUser.ID
	mismatched.StoragePath = "hermes-restaurant-audio/audio-mismatch.wav"
	if err := dataStore.CreateHermesRestaurantAudio(
		ctx,
		mismatched,
	); err == nil {
		t.Fatal("created audio with mismatched credential owner")
	}
	traversal := first
	traversal.ID = "audio-traversal"
	traversal.StoragePath = "../outside.wav"
	if err := dataStore.CreateHermesRestaurantAudio(
		ctx,
		traversal,
	); err == nil {
		t.Fatal("created path-traversing audio record")
	}
	wrongRoot := first
	wrongRoot.ID = "audio-wrong-root"
	wrongRoot.StoragePath = "uploads/audio-wrong-root.wav"
	if err := dataStore.CreateHermesRestaurantAudio(
		ctx,
		wrongRoot,
	); err == nil {
		t.Fatal("created audio record outside the dedicated audio directory")
	}

	expired := first
	expired.ID = "audio-expired"
	expired.RequestKey = "request-expired"
	expired.StoragePath = "hermes-restaurant-audio/audio-expired.wav"
	expired.CreatedAt = now - 20_000
	expired.ExpiresAt = now - 10_000
	if err := dataStore.CreateHermesRestaurantAudio(ctx, expired); err != nil {
		t.Fatal(err)
	}
	paths, err := dataStore.PurgeExpiredHermesRestaurantAudio(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != expired.StoragePath {
		t.Fatalf("purged audio paths = %#v", paths)
	}
	if _, err := dataStore.HermesRestaurantAudioByID(
		ctx,
		firstCredential.ID,
		firstUser.ID,
		expired.ID,
		now-15_000,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged audio lookup error = %v", err)
	}

	deletedPaths, err := dataStore.DeleteHermesRestaurantAudio(
		ctx,
		[]string{first.ID, second.ID, "missing"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(deletedPaths) != 2 {
		t.Fatalf("deleted audio paths = %#v", deletedPaths)
	}
}

func createHermesRestaurantUser(
	t *testing.T,
	dataStore *Store,
	username string,
) User {
	t.Helper()
	user, err := dataStore.CreateUser(
		context.Background(),
		username,
		username,
		"hash",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		context.Background(),
		username,
		guidance.WorkbenchRestaurant,
		"",
	); err != nil {
		t.Fatal(err)
	}
	return user
}
