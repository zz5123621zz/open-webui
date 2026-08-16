package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateConversationUnlimitedSkipsRetention(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "unlimited", "Unlimited", "hash")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		if _, err := dataStore.CreateConversationWithLimit(
			ctx, user.ID, "Chat", "gpt-test", "high", 0,
		); err != nil {
			t.Fatalf("create %d: %v", index, err)
		}
	}
	active, err := dataStore.ListConversations(ctx, user.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 5 {
		t.Fatalf("active conversations = %d, want 5", len(active))
	}
}

func TestRestoreConversationUnlimitedIgnoresCount(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "restore-unlimited", "Restore", "hash")
	if err != nil {
		t.Fatal(err)
	}
	first, err := dataStore.CreateConversationWithLimit(ctx, user.ID, "One", "gpt-test", "high", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateConversationWithLimit(ctx, user.ID, "Two", "gpt-test", "high", 1); err != nil {
		t.Fatal(err)
	}
	retained, err := dataStore.OwnedConversationByID(ctx, user.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retained.ArchivedAt == 0 {
		t.Fatalf("expected first conversation to be retained: %#v", retained)
	}
	if _, err := dataStore.SetConversationArchivedWithPolicy(
		ctx, user.ID, first.ID, false, 1, defaultMaxStorageBytes,
	); !errors.Is(err, ErrConversationLimit) {
		t.Fatalf("restore with limit = %v, want ErrConversationLimit", err)
	}
	restored, err := dataStore.SetConversationArchivedWithPolicy(
		ctx, user.ID, first.ID, false, 0, defaultMaxStorageBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ArchivedAt != 0 {
		t.Fatalf("unlimited restore left conversation archived: %#v", restored)
	}
}

func TestConversationLimitSettingRequiresAdmin(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "limit-user", "User", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetConversationLimitSetting(ctx, user.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("regular user update = %v, want ErrNotFound", err)
	}
	admin, err := dataStore.CreateUserWithRole(ctx, "limit-admin", "Admin", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ConversationLimitSetting(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unset limit = %v, want ErrNotFound", err)
	}
	setting, err := dataStore.SetConversationLimitSetting(ctx, admin.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if setting.MaxActive != 0 || setting.UpdatedBy != admin.ID {
		t.Fatalf("saved setting = %#v", setting)
	}
	loaded, err := dataStore.ConversationLimitSetting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MaxActive != 0 {
		t.Fatalf("loaded setting = %#v", loaded)
	}
	if _, err := dataStore.SetConversationLimitSetting(ctx, admin.ID, -1); err == nil {
		t.Fatal("negative limit was accepted")
	}
}
