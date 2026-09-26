import { useAuth } from "@goerp/sdk/auth";
import { ActionMenu, UserAvatar } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import { useTheme } from "@goerp/sdk/react";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown } from "lucide-react";
import { type ReactNode, useRef } from "react";
import { useDueActivityCount } from "../activities/use-due-activity-count.js";
import { isTenantAdmin } from "../admin/is-tenant-admin.js";
import { openKeyboardShortcuts } from "../shortcuts/keyboard-shortcuts-control.js";
import { titleCaseWords } from "./title-case-words.js";

// CurrentUser.name is null for a user with no system.user_profiles row
// (pre-goerp#817 users, or any invite path other than tenant provisioning
// until a general invite endpoint exists) — derives a presentable value
// from the email local-part instead ("jane.doe" -> "Jane Doe"). Falls
// back to the raw email if the local-part title-cases to empty (e.g.
// "@example.com").
function displayNameFromEmail(email: string): string {
  const local = email.split("@")[0] ?? "";
  return titleCaseWords(local, /[._-]/) || email;
}

// chrome-header.md's UserMenu: reuses ActionMenu's panel mechanics via its
// `trigger` slot instead of the default labeled button.
export function UserMenu(): ReactNode {
  const { user, logout, updatePreferences } = useAuth();
  const { theme, preference, setPreference } = useTheme();
  const navigate = useNavigate();
  const dueActivities = useDueActivityCount();

  // Only the latest toggle's failure reverts, so an earlier save settling
  // late can't undo a later toggle.
  const latestToggle = useRef(0);

  // Saved to the profile like the Appearance page's theme control
  // (shell-ux.md §4.4), so the next sign-in doesn't undo it.
  function toggleDarkMode() {
    const previous = preference;
    const next = theme === "dark" ? "light" : "dark";
    const token = ++latestToggle.current;
    setPreference(next);
    updatePreferences({ theme: next }).catch(() => {
      if (latestToggle.current !== token) return;
      setPreference(previous);
      toast.error("Couldn't save your theme. Try again.");
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
        { label: "Dark mode", checked: theme === "dark", onClick: toggleDarkMode },
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
          className="flex items-center gap-1.5 rounded-control p-1 hover:bg-surface-hover focus-visible:outline-none focus-visible:shadow-focus"
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
