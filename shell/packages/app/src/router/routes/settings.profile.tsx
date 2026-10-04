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

export const Route = createFileRoute("/settings/profile")({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } =>
    typeof search.redirect === "string" ? { redirect: search.redirect } : {},
  component: ProfilePage,
});

// The sentinel hydrates the existing avatar; only a newly uploaded file ID is saved.
function currentAvatarValue(avatarUrl: string | null): FileValue | null {
  if (!avatarUrl) return null;
  return { fileId: "current", name: "avatar", contentType: "", sizeBytes: 0, url: avatarUrl };
}

function ProfilePage() {
  const user = useUser();
  const { updateProfile } = useAuth();

  const [name, setName] = useState(user.name ?? "");
  const [phone, setPhone] = useState(user.phone ?? "");
  const [title, setTitle] = useState(user.title ?? "");
  const [avatarValue, setAvatarValue] = useState<FileValue | null>(currentAvatarValue(user.avatarUrl));
  const [avatarChanged, setAvatarChanged] = useState(false);
  const [saving, setSaving] = useState(false);

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
        name: trimmed,
        phone: phone.trim() || null,
        title: title.trim() || null,
        // An omitted avatar keeps it; an empty ID clears it.
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
      <PageHeader title="Profile" subtitle="Manage your profile and password." />
      <div className="flex max-w-md flex-col gap-6">
        <div className="flex flex-col gap-2">
          <span className="font-medium text-sm text-text">Avatar</span>
          <FileField variant="avatar" value={avatarValue} onChange={handleAvatarChange} />
        </div>
        <FieldWrapper
          label="Full name"
          description="Your name and photo are shared with every organisation you belong to."
        >
          <TextInput autoComplete="name" value={name} onChange={setName} />
        </FieldWrapper>
        <div className="flex flex-col gap-2">
          <span className="font-medium text-sm text-text">Email</span>
          <p className="text-sm text-text-secondary">{user.email}</p>
        </div>
        <FieldWrapper label="Phone">
          <TextInput type="tel" autoComplete="tel" value={phone} onChange={setPhone} maxLength={64} />
        </FieldWrapper>
        <FieldWrapper label="Job title">
          <TextInput autoComplete="organization-title" value={title} onChange={setTitle} maxLength={200} />
        </FieldWrapper>
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
