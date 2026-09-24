import { IconButton } from "@goerp/sdk/components";
import { useUnreadCount } from "@goerp/sdk/notifications";
import type { ReactNode } from "react";
import { useState } from "react";
import { NotificationSheet } from "./notification-sheet.js";

// §19's badge hardcodes text-white; corrected to --color-text-inverse
// (ActionButton's 4.5:1 contrast rule fails white-on-danger in dark mode).
export function NotificationBell(): ReactNode {
  const { count } = useUnreadCount();
  const [open, setOpen] = useState(false);
  const capped = count > 9 ? "9+" : String(count);

  return (
    <>
      <span className="relative inline-flex">
        <IconButton
          icon="bell"
          label={count > 0 ? `Notifications (${count} unread)` : "Notifications"}
          aria-expanded={open}
          onClick={() => setOpen((current) => !current)}
        />
        {count > 0 && (
          <span
            aria-hidden="true"
            className="pointer-events-none -top-0.5 -right-0.5 absolute flex h-4 w-4 items-center justify-center rounded-full bg-danger text-[9px] font-bold text-text-inverse"
          >
            {capped}
          </span>
        )}
      </span>

      <NotificationSheet open={open} onClose={() => setOpen(false)} />
    </>
  );
}
