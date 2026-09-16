import { useAuth } from "@goerp/sdk/auth";
import { ActionMenu, UserAvatar } from "@goerp/sdk/components";
import { useTheme } from "@goerp/sdk/react";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown } from "lucide-react";
import type { ReactNode } from "react";
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
  const { user, logout } = useAuth();
  const { theme, toggleTheme } = useTheme();
  const navigate = useNavigate();

  if (!user) return null;
  const displayName = user.name || displayNameFromEmail(user.email);
  // Routed through a string-typed parameter: these three routes don't
  // exist yet, and a literal `to` not in the route tree fails typecheck.
  const goTo = (path: string) => void navigate({ to: path });

  return (
    <ActionMenu
      label={displayName}
      items={[
        { label: "Profile", onClick: () => goTo("/profile") },
        { label: "Settings", onClick: () => goTo("/settings") },
        { label: "Dark mode", checked: theme === "dark", onClick: toggleTheme },
        { label: "Keyboard shortcuts", onClick: () => goTo("/keyboard-shortcuts") },
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
