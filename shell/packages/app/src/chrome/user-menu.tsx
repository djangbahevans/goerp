import { useAuth } from "@goerp/sdk/auth";
import { ActionMenu, UserAvatar } from "@goerp/sdk/components";
import { useTheme } from "@goerp/sdk/react";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown } from "lucide-react";
import type { CSSProperties, ReactNode } from "react";
import { titleCaseWords } from "./title-case-words.js";

// rotate-180 generates no CSS in this project's @tailwindcss/vite setup
// (confirmed empirically, goerp#729) — inline style instead.
function chevronStyle(open: boolean): CSSProperties {
  return { transform: open ? "rotate(180deg)" : undefined };
}

// CurrentUser has no `name` field, only `email` — UserAvatar's `name` is
// required, so this derives a presentable value ("jane.doe" -> "Jane Doe").
// Falls back to the raw email if the local-part title-cases to empty
// (e.g. "@example.com").
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
  const displayName = displayNameFromEmail(user.email);
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
          <UserAvatar name={displayName} size="sm" />
          <ChevronDown
            size={14}
            aria-hidden="true"
            style={chevronStyle(open)}
            className="text-text-secondary transition-transform duration-(--duration-fast)"
          />
        </button>
      )}
    />
  );
}
