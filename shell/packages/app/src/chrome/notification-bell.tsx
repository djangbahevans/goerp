import { useUnreadCount } from "@goerp/sdk/notifications";
import { Bell } from "lucide-react";
import type { CSSProperties, ReactNode } from "react";
import { useState } from "react";
import { NotificationSheet } from "./notification-sheet.js";

// Individual top/right offsets generate no CSS at all in this project's
// @tailwindcss/vite setup (confirmed empirically — see notification-sheet.tsx),
// so the badge's position is inline style instead of -top-0.5 -right-0.5.
const BADGE_STYLE: CSSProperties = { top: "-2px", right: "-2px" };

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
        className="relative rounded-control p-1.5 hover:bg-surface-hover aria-expanded:bg-surface-hover"
        aria-expanded={open}
      >
        <Bell size={18} aria-hidden="true" />
        {count > 0 && (
          <span
            aria-hidden="true"
            style={BADGE_STYLE}
            className="absolute flex h-4 w-4 items-center justify-center rounded-full bg-danger text-[9px] font-bold text-text-inverse"
          >
            {capped}
          </span>
        )}
      </button>

      <NotificationSheet open={open} onClose={() => setOpen(false)} />
    </>
  );
}
