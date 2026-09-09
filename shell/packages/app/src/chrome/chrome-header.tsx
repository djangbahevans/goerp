import type { CSSProperties, ReactNode } from "react";
import { NotificationBell } from "./notification-bell.js";
import { RouteBreadcrumb } from "./route-breadcrumb.js";
import { SearchTrigger } from "./search-trigger.js";
import { UserMenu } from "./user-menu.js";

// h-(--header-height) generates no CSS at all in this project's
// @tailwindcss/vite setup (confirmed empirically, goerp#729) — inline
// style instead.
const HEADER_STYLE: CSSProperties = { height: "var(--header-height)" };

// shell-architecture.md §15/§17: the shell's persistent global header,
// mounted once by ChromeLayout (unbuilt, a later ticket). No explicit
// role="banner" — a top-level <header> carries that landmark implicitly.
export function ChromeHeader(): ReactNode {
  return (
    <header style={HEADER_STYLE} className="flex items-center justify-between gap-4 border-border border-b bg-bg px-4">
      <RouteBreadcrumb />
      <div className="flex items-center gap-2">
        <SearchTrigger />
        <NotificationBell />
        <UserMenu />
      </div>
    </header>
  );
}
