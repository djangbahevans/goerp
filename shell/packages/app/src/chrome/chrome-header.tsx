import type { ReactNode } from "react";
import { NotificationBell } from "./notification-bell.js";
import { RouteBreadcrumb } from "./route-breadcrumb.js";
import { SearchTrigger } from "./search-trigger.js";
import { UserMenu } from "./user-menu.js";

// shell-architecture.md §15/§17: the shell's persistent global header,
// mounted once by ChromeLayout (unbuilt, a later ticket). No explicit
// role="banner" — a top-level <header> carries that landmark implicitly.
export function ChromeHeader(): ReactNode {
  return (
    <header className="flex h-(--header-height) items-center justify-between gap-4 border-border border-b bg-bg px-4">
      <RouteBreadcrumb />
      <div className="flex items-center gap-2">
        <SearchTrigger />
        <NotificationBell />
        <UserMenu />
      </div>
    </header>
  );
}
