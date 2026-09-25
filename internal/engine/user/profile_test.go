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

func TestUpdateProfile_CreatesRowWhenNoneExists(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	fileID := "00000000-0000-7000-8000-000000000001"
	old, err := store.UpdateProfile(context.Background(), userID, ProfileUpdate{Name: new("Ada Lovelace"), AvatarFileID: &fileID})
	if err != nil {
		t.Fatalf("UpdateProfile() error: %v", err)
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

func TestUpdateProfile_OverwritesExistingName(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	if err := store.EnsureProfile(context.Background(), userID, "Old Name"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}
	if _, err := store.UpdateProfile(context.Background(), userID, ProfileUpdate{Name: new("New Name")}); err != nil {
		t.Fatalf("UpdateProfile() error: %v", err)
	}

	profile, err := store.GetProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "New Name" {
		t.Errorf("Name = %q, want %q (UpdateProfile must overwrite an existing name)", profile.Name, "New Name")
	}
}

func TestUpdateProfile_NilAvatarFileIDPreservesExistingAvatar(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	fileID := "00000000-0000-7000-8000-000000000001"
	if _, err := store.UpdateProfile(context.Background(), userID, ProfileUpdate{Name: new("First Save"), AvatarFileID: &fileID}); err != nil {
		t.Fatalf("first UpdateProfile() error: %v", err)
	}
	// old is still reported as fileID here — UpdateProfile always
	// reports whatever avatar was in place before the call, whether or
	// not this call's own avatarFileID touched it. It's the caller's job
	// (authmeupdate.Handler) to only act on old when its own request
	// actually provided a new avatar value, which this call's nil didn't.
	old, err := store.UpdateProfile(context.Background(), userID, ProfileUpdate{Name: new("Second Save")})
	if err != nil {
		t.Fatalf("second UpdateProfile() error: %v", err)
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

func TestUpdateProfile_EmptyStringAvatarFileIDClearsExistingAvatar(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	fileID := "00000000-0000-7000-8000-000000000001"
	if _, err := store.UpdateProfile(context.Background(), userID, ProfileUpdate{Name: new("First Save"), AvatarFileID: &fileID}); err != nil {
		t.Fatalf("first UpdateProfile() error: %v", err)
	}
	old, err := store.UpdateProfile(context.Background(), userID, ProfileUpdate{Name: new("Second Save"), AvatarFileID: new(string)})
	if err != nil {
		t.Fatalf("second UpdateProfile() error: %v", err)
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

func TestUpdateProfile_ReturnsPriorAvatarWhenReplaced(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	first := "00000000-0000-7000-8000-000000000001"
	second := "00000000-0000-7000-8000-000000000002"
	if _, err := store.UpdateProfile(context.Background(), userID, ProfileUpdate{Name: new("Name"), AvatarFileID: &first}); err != nil {
		t.Fatalf("first UpdateProfile() error: %v", err)
	}
	old, err := store.UpdateProfile(context.Background(), userID, ProfileUpdate{Name: new("Name"), AvatarFileID: &second})
	if err != nil {
		t.Fatalf("second UpdateProfile() error: %v", err)
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

func TestUpdateProfile_PreferencesOnlyLeaveNameAndAvatarUntouched(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	fileID := "00000000-0000-7000-8000-000000000001"
	if _, err := store.UpdateProfile(t.Context(), userID, ProfileUpdate{Name: new("Ada"), AvatarFileID: &fileID}); err != nil {
		t.Fatalf("first UpdateProfile() error: %v", err)
	}
	if _, err := store.UpdateProfile(t.Context(), userID, ProfileUpdate{
		Theme:      new("dark"),
		Locale:     NullableField{Set: true, Value: new("fr")},
		Timezone:   NullableField{Set: true, Value: new("Africa/Accra")},
		DateFormat: NullableField{Set: true, Value: new("iso")},
	}); err != nil {
		t.Fatalf("second UpdateProfile() error: %v", err)
	}

	profile, err := store.GetProfile(t.Context(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "Ada" || profile.AvatarFileID == nil || *profile.AvatarFileID != fileID {
		t.Errorf("Name/AvatarFileID = %q/%v, want Ada/%q untouched", profile.Name, profile.AvatarFileID, fileID)
	}
	if profile.Theme != "dark" || deref(profile.Locale) != "fr" || deref(profile.Timezone) != "Africa/Accra" || deref(profile.DateFormat) != "iso" {
		t.Errorf("preferences = %q/%q/%q/%q, want dark/fr/Africa/Accra/iso", profile.Theme, deref(profile.Locale), deref(profile.Timezone), deref(profile.DateFormat))
	}
}

func TestUpdateProfile_NullResetsAndUnsetLeavesPreferences(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	if _, err := store.UpdateProfile(t.Context(), userID, ProfileUpdate{
		Name:     new("Ada"),
		Locale:   NullableField{Set: true, Value: new("fr")},
		Timezone: NullableField{Set: true, Value: new("Africa/Accra")},
	}); err != nil {
		t.Fatalf("first UpdateProfile() error: %v", err)
	}
	if _, err := store.UpdateProfile(t.Context(), userID, ProfileUpdate{
		Locale: NullableField{Set: true},
	}); err != nil {
		t.Fatalf("second UpdateProfile() error: %v", err)
	}

	profile, err := store.GetProfile(t.Context(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Locale != nil {
		t.Errorf("Locale = %q, want nil (a null reset)", *profile.Locale)
	}
	if deref(profile.Timezone) != "Africa/Accra" {
		t.Errorf("Timezone = %v, want Africa/Accra (unset fields stay)", profile.Timezone)
	}
}

func TestGetProfile_DefaultsThemeToSystem(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	if err := store.EnsureProfile(t.Context(), userID, "Ada"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}
	profile, err := store.GetProfile(t.Context(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Theme != "system" || profile.Locale != nil || profile.Timezone != nil || profile.DateFormat != nil {
		t.Errorf("defaults = %q/%v/%v/%v, want system and three nils", profile.Theme, profile.Locale, profile.Timezone, profile.DateFormat)
	}
}

func TestUpdateProfile_CreatesRowWithoutNameAsPlaceholder(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	if _, err := store.UpdateProfile(t.Context(), userID, ProfileUpdate{Theme: new("light")}); err != nil {
		t.Fatalf("UpdateProfile() error: %v", err)
	}
	profile, err := store.GetProfile(t.Context(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "" || profile.Theme != "light" {
		t.Errorf("Name/Theme = %q/%q, want empty placeholder name and light", profile.Name, profile.Theme)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestEnsureProfile_FillsPlaceholderName(t *testing.T) {
	store, _ := openTestStore(t)
	userID := createFixtureUser(t, store)

	if _, err := store.UpdateProfile(t.Context(), userID, ProfileUpdate{Theme: new("dark")}); err != nil {
		t.Fatalf("UpdateProfile() error: %v", err)
	}
	profile, err := store.GetProfile(t.Context(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.DisplayName() != nil {
		t.Fatalf("DisplayName() = %q, want nil for the placeholder", *profile.DisplayName())
	}

	if err := store.EnsureProfile(t.Context(), userID, "Ada"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}
	profile, err = store.GetProfile(t.Context(), userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if name := profile.DisplayName(); name == nil || *name != "Ada" || profile.Theme != "dark" {
		t.Errorf("name/theme = %v/%q, want Ada (placeholder filled) and dark kept", name, profile.Theme)
	}
}
