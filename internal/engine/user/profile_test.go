package user

import (
	"context"
	"errors"
	"testing"
)

func createFixtureUser(t *testing.T, store *Store) string {
	t.Helper()
	id, err := store.FindOrCreateInvited(context.Background(), uniqueEmail(t))
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = store.db.Exec(`DELETE FROM system.users WHERE id = $1`, id) })
	return id
}

func TestGetProfile_ReturnsErrProfileNotFoundForMissingRow(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	_, err := store.GetProfile(context.Background(), userID)
	if !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("GetProfile() error = %v, want ErrProfileNotFound", err)
	}
}

func TestEnsureProfile_CreatesRowThenGetProfileReturnsIt(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	if err := store.EnsureProfile(context.Background(), userID, "Grace Hopper"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}

	profile, err := store.GetProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "Grace Hopper" {
		t.Errorf("Name = %q, want %q", profile.Name, "Grace Hopper")
	}
	if profile.AvatarURL != nil {
		t.Errorf("AvatarURL = %v, want nil", *profile.AvatarURL)
	}
}

func TestEnsureProfile_DoesNotOverwriteExistingName(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	if err := store.EnsureProfile(context.Background(), userID, "First Name"); err != nil {
		t.Fatalf("first EnsureProfile() error: %v", err)
	}
	if err := store.EnsureProfile(context.Background(), userID, "Second Name"); err != nil {
		t.Fatalf("second EnsureProfile() error: %v", err)
	}

	profile, err := store.GetProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "First Name" {
		t.Errorf("Name = %q, want %q (EnsureProfile must not overwrite an existing row)", profile.Name, "First Name")
	}
}
