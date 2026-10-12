import { PermissionContext, useAuth } from "@goerp/sdk/auth";
import { ActionMenu, UserAvatar } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import { useTheme } from "@goerp/sdk/react";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown } from "lucide-react";
import { type ReactNode, useContext, useRef } from "react";
import { useDueActivityCount } from "../activities/use-due-activity-count.js";
import { isTenantAdmin } from "../admin/is-tenant-admin.js";
import { OVERRIDE_PERMISSION } from "../admin/view-overrides/view-elements.js";
import { openKeyboardShortcuts } from "../shortcuts/keyboard-shortcuts-control.js";
import { titleCaseWords } from "./title-case-words.js";

function displayNameFromEmail(email: string): string {
  const local = email.split("@")[0] ?? "";
  return titleCaseWords(local, /[._-]/) || email;
}

export function UserMenu(): ReactNode {
  const { user, logout, updatePreferences } = useAuth();
  const { theme, preference, setPreference, contrast, contrastPreference, setContrastPreference } = useTheme();
  const navigate = useNavigate();
  const permissions = useContext(PermissionContext);
  const dueActivities = useDueActivityCount();

  // Per toggle, only the latest one's failure reverts, so an earlier save
  // settling late can't undo a later toggle.
  const latestToggle = useRef({ theme: 0, contrast: 0 });

  function toggleDarkMode() {
    const previous = preference;
    const next = theme === "dark" ? "light" : "dark";
    const token = ++latestToggle.current.theme;
    setPreference(next);
    updatePreferences({ theme: next }).catch(() => {
      if (latestToggle.current.theme !== token) return;
      setPreference(previous);
      toast.error("Couldn't save your theme. Try again.");
    });
  }

  function toggleHighContrast() {
    const previous = contrastPreference;
    const next = contrast === "high" ? "standard" : "high";
    const token = ++latestToggle.current.contrast;
    setContrastPreference(next);
    updatePreferences({ contrast: next }).catch(() => {
      if (latestToggle.current.contrast !== token) return;
      setContrastPreference(previous);
      toast.error("Couldn't save your contrast. Try again.");
    });
  }

  if (!user) return null;
  const displayName = user.name || displayNameFromEmail(user.email);
  return (
    <ActionMenu
      label={displayName}
      items={[
        { label: "My activities", badge: dueActivities, onClick: () => void navigate({ to: "/activities" }) },
        { label: "Profile", onClick: () => void navigate({ to: "/settings/profile" }) },
        { label: "Settings", onClick: () => void navigate({ to: "/settings" }) },
        ...(isTenantAdmin(user) ? [{ label: "Admin", onClick: () => void navigate({ to: "/admin" }) }] : []),
        ...(!isTenantAdmin(user) && permissions?.check(OVERRIDE_PERMISSION)
          ? [{ label: "View overrides", onClick: () => void navigate({ to: "/admin/view-overrides" }) }]
          : []),
        { label: "Dark mode", checked: theme === "dark", onClick: toggleDarkMode },
        { label: "High contrast", checked: contrast === "high", onClick: toggleHighContrast },
        { label: "Keyboard shortcuts", onClick: openKeyboardShortcuts },
        { type: "separator" },
        { label: "Sign out", onClick: () => void logout() },
      ]}
      trigger={({ ref, open, onClick, onKeyDown }) => (
        <button
          ref={ref}
          type="button"
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label={`${displayName}'s account menu`}
          onClick={onClick}
          onKeyDown={onKeyDown}
          className="flex items-center gap-1.5 rounded-control p-1 hover:bg-surface-hover focus-visible:shadow-focus focus-visible:outline-none max-md:min-h-11 max-md:min-w-11 max-md:justify-center"
        >
          <UserAvatar userId={user.id} name={displayName} avatarUrl={user.avatarUrl} size="sm" />
          <ChevronDown
            size={14}
            aria-hidden="true"
            className={`text-text-secondary transition-transform duration-(--duration-fast) ${open ? "rotate-180" : ""}`}
          />
        </button>
      )}
    />
  );
}
