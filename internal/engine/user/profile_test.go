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
	if profile.AvatarFileID != nil {
		t.Errorf("AvatarFileID = %v, want nil", *profile.AvatarFileID)
	}
}

func TestReplaceProfile_CreatesRowWhenNoneExists(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	fileID := "00000000-0000-7000-8000-000000000001"
	old, err := store.ReplaceProfile(context.Background(), userID, "Ada Lovelace", &fileID)
	if err != nil {
		t.Fatalf("ReplaceProfile() error: %v", err)
	}
	if old != nil {
		t.Errorf("old avatar = %v, want nil (no prior profile)", *old)
	}

	profile, err := store.GetProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "Ada Lovelace" {
		t.Errorf("Name = %q, want %q", profile.Name, "Ada Lovelace")
	}
	if profile.AvatarFileID == nil || *profile.AvatarFileID != fileID {
		t.Errorf("AvatarFileID = %v, want %q", profile.AvatarFileID, fileID)
	}
}

func TestReplaceProfile_OverwritesExistingName(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	if err := store.EnsureProfile(context.Background(), userID, "Old Name"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}
	if _, err := store.ReplaceProfile(context.Background(), userID, "New Name", nil); err != nil {
		t.Fatalf("ReplaceProfile() error: %v", err)
	}

	profile, err := store.GetProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "New Name" {
		t.Errorf("Name = %q, want %q (ReplaceProfile must overwrite an existing name)", profile.Name, "New Name")
	}
}

func TestReplaceProfile_NilAvatarFileIDPreservesExistingAvatar(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	fileID := "00000000-0000-7000-8000-000000000001"
	if _, err := store.ReplaceProfile(context.Background(), userID, "First Save", &fileID); err != nil {
		t.Fatalf("first ReplaceProfile() error: %v", err)
	}
	// old is still reported as fileID here — ReplaceProfile always
	// reports whatever avatar was in place before the call, whether or
	// not this call's own avatarFileID touched it. It's the caller's job
	// (authmeupdate.Handler) to only act on old when its own request
	// actually provided a new avatar value, which this call's nil didn't.
	old, err := store.ReplaceProfile(context.Background(), userID, "Second Save", nil)
	if err != nil {
		t.Fatalf("second ReplaceProfile() error: %v", err)
	}
	if old == nil || *old != fileID {
		t.Errorf("old avatar = %v, want %q", old, fileID)
	}

	profile, err := store.GetProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "Second Save" {
		t.Errorf("Name = %q, want %q", profile.Name, "Second Save")
	}
	if profile.AvatarFileID == nil || *profile.AvatarFileID != fileID {
		t.Errorf("AvatarFileID = %v, want %q (a nil avatarFileID on save must not clear an existing avatar)", profile.AvatarFileID, fileID)
	}
}

func TestReplaceProfile_EmptyStringAvatarFileIDClearsExistingAvatar(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	fileID := "00000000-0000-7000-8000-000000000001"
	if _, err := store.ReplaceProfile(context.Background(), userID, "First Save", &fileID); err != nil {
		t.Fatalf("first ReplaceProfile() error: %v", err)
	}
	old, err := store.ReplaceProfile(context.Background(), userID, "Second Save", new(string))
	if err != nil {
		t.Fatalf("second ReplaceProfile() error: %v", err)
	}
	if old == nil || *old != fileID {
		t.Errorf("old avatar = %v, want %q", old, fileID)
	}

	profile, err := store.GetProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.AvatarFileID != nil {
		t.Errorf("AvatarFileID = %v, want nil (an empty-string avatarFileID must clear it)", *profile.AvatarFileID)
	}
}

func TestReplaceProfile_ReturnsPriorAvatarWhenReplaced(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	first := "00000000-0000-7000-8000-000000000001"
	second := "00000000-0000-7000-8000-000000000002"
	if _, err := store.ReplaceProfile(context.Background(), userID, "Name", &first); err != nil {
		t.Fatalf("first ReplaceProfile() error: %v", err)
	}
	old, err := store.ReplaceProfile(context.Background(), userID, "Name", &second)
	if err != nil {
		t.Fatalf("second ReplaceProfile() error: %v", err)
	}
	if old == nil || *old != first {
		t.Errorf("old avatar = %v, want %q", old, first)
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
