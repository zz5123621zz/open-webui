package store

import (
	"context"
	"errors"
	"testing"
)

func TestDictationSettingDefaultsEnabledAndRequiresAdministrator(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(
		ctx,
		"dictation-user",
		"Dictation User",
		"hash",
	)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := dataStore.CreateUserWithRole(
		ctx,
		"dictation-admin",
		"Dictation Admin",
		"hash",
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}

	var migrationCount int
	if err := dataStore.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM schema_migrations WHERE version = 8
	`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("schema v8 migration count = %d, want 1", migrationCount)
	}
	initial, err := dataStore.DictationServiceSetting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !initial.Enabled || initial.UpdatedAt <= 0 || initial.UpdatedBy != "" {
		t.Fatalf("initial dictation setting = %#v", initial)
	}
	if _, err := dataStore.SetDictationServiceSetting(
		ctx,
		user.ID,
		false,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("member update error = %v, want ErrNotFound", err)
	}

	disabled, err := dataStore.SetDictationServiceSetting(
		ctx,
		admin.ID,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled ||
		disabled.UpdatedBy != admin.ID ||
		disabled.UpdatedAt < initial.UpdatedAt {
		t.Fatalf("disabled dictation setting = %#v", disabled)
	}
	if _, err := dataStore.SetDictationServiceSetting(
		ctx,
		admin.ID,
		false,
	); err != nil {
		t.Fatal(err)
	}
	enabled, err := dataStore.SetDictationServiceSetting(
		ctx,
		admin.ID,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled {
		t.Fatalf("enabled dictation setting = %#v", enabled)
	}

	rows, err := dataStore.db.QueryContext(ctx, `
		SELECT old_enabled, new_enabled, actor_user_id
		FROM dictation_service_setting_audit
		ORDER BY rowid ASC
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type audit struct {
		old, next int
		actor     string
	}
	var audits []audit
	for rows.Next() {
		var item audit
		if err := rows.Scan(&item.old, &item.next, &item.actor); err != nil {
			t.Fatal(err)
		}
		audits = append(audits, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(audits) != 2 ||
		audits[0].old != 1 ||
		audits[0].next != 0 ||
		audits[0].actor != admin.ID ||
		audits[1].old != 0 ||
		audits[1].next != 1 ||
		audits[1].actor != admin.ID {
		t.Fatalf("dictation setting audit = %#v", audits)
	}
}
