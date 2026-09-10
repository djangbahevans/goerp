import { useUnreadCount } from "@goerp/sdk/notifications";
import { Bell } from "lucide-react";
import type { CSSProperties, ReactNode } from "react";
import { useState } from "react";
import { NotificationSheet } from "./notification-sheet.js";

// -top-0.5/-right-0.5 (negative fractional inset), text-[9px] (arbitrary
// bracket font-size), and bare p-1.5 (fractional padding) all generate no
// CSS in this build (goerp#729) — inline styles instead. w-4 works fine
// (bare integer width isn't affected), set via className below instead.
const BADGE_STYLE: CSSProperties = {
  top: "-2px",
  right: "-2px",
  fontSize: "9px",
};
const BUTTON_STYLE: CSSProperties = { padding: "6px" };

// §19's badge hardcodes text-white; corrected to --color-text-inverse
// (ActionButton's 4.5:1 contrast rule fails white-on-danger in dark mode).
export function NotificationBell(): ReactNode {
  const { count } = useUnreadCount();
  const [open, setOpen] = useState(false);
  const capped = count > 9 ? "9+" : String(count);

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-label={count > 0 ? `Notifications (${count} unread)` : "Notifications"}
        style={BUTTON_STYLE}
        className="relative rounded-control hover:bg-surface-hover aria-expanded:bg-surface-hover"
        aria-expanded={open}
      >
        <Bell size={18} aria-hidden="true" />
        {count > 0 && (
          <span
            aria-hidden="true"
            style={BADGE_STYLE}
            className="absolute flex h-4 w-4 items-center justify-center rounded-full bg-danger font-bold text-text-inverse"
          >
            {capped}
          </span>
        )}
      </button>

      <NotificationSheet open={open} onClose={() => setOpen(false)} />
    </>
  );
}
