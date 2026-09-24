import { useAuth, useUser } from "@goerp/sdk/auth";
import {
  ActionButton,
  FieldWrapper,
  FileField,
  type FileValue,
  PageHeader,
  PageLayout,
  TextInput,
} from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { ChangePasswordSection } from "../../settings/change-password-section.js";

// shell-ux.md §4.1 — name, avatar, and change password. Phone/title and
// change-email have no backing columns or endpoints yet.
export const Route = createFileRoute("/settings/profile")({
  component: ProfilePage,
});

// A placeholder FileValue hydrating FileField from the already-resolved
// avatarUrl GET /auth/me returns — fileId "current" is never sent back to
// the server (handleSave only reads avatarValue.fileId when avatarChanged
// is true, which only becomes true from a real onChange, i.e. a new
// upload replacing this placeholder).
function currentAvatarValue(avatarUrl: string | null): FileValue | null {
  if (!avatarUrl) return null;
  return { fileId: "current", name: "avatar", contentType: "", sizeBytes: 0, url: avatarUrl };
}

function ProfilePage() {
  const user = useUser();
  const { updateProfile } = useAuth();

  const [name, setName] = useState(user.name ?? "");
  const [avatarValue, setAvatarValue] = useState<FileValue | null>(currentAvatarValue(user.avatarUrl));
  const [avatarChanged, setAvatarChanged] = useState(false);
  const [saving, setSaving] = useState(false);

  // FileField's avatar variant never sets multiple, so value is always
  // FileValue | null here — Array.isArray is just a type-narrowing guard,
  // not an expected runtime path.
  const handleAvatarChange = (value: FileValue | FileValue[] | null) => {
    setAvatarValue(Array.isArray(value) ? null : value);
    setAvatarChanged(true);
  };

  const handleSave = async () => {
    const trimmed = name.trim();
    if (!trimmed) {
      toast.error("Name is required.");
      return;
    }
    setSaving(true);
    try {
      await updateProfile({
        // "" is a real, distinct signal from undefined here (goerp#819
        // review) — undefined means "leave the avatar alone" (the key is
        // dropped from the PATCH body entirely, auth-client.ts), while ""
        // means "clear it" (avatarValue is null after the user removed
        // their avatar via FileField's own remove button, which is a real
        // avatarChanged event distinct from never having touched it).
        name: trimmed,
        avatarId: avatarChanged ? (avatarValue?.fileId ?? "") : undefined,
      });
      toast.success("Profile updated.");
      setAvatarChanged(false);
    } catch {
      toast.error("Couldn't save your profile. Try again.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <PageLayout>
      <PageHeader title="Profile" subtitle="Manage your name, avatar, and password." />
      <div className="flex max-w-md flex-col gap-6">
        <div className="flex flex-col gap-2">
          <span className="font-medium text-sm text-text">Avatar</span>
          <FileField variant="avatar" value={avatarValue} onChange={handleAvatarChange} />
        </div>
        <FieldWrapper label="Full name">
          <TextInput autoComplete="name" value={name} onChange={setName} />
        </FieldWrapper>
        <div className="flex flex-col gap-2">
          <span className="font-medium text-sm text-text">Email</span>
          <p className="text-sm text-text-secondary">{user.email}</p>
        </div>
        <div>
          <ActionButton onClick={() => void handleSave()} loading={saving} disabled={saving}>
            Save changes
          </ActionButton>
        </div>
      </div>
      <div className="mt-8 max-w-md">
        <ChangePasswordSection />
      </div>
    </PageLayout>
  );
}
