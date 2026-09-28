import type { ReactNode } from "react";
import { HelpButton } from "./help-button.js";
import { NotificationBell } from "./notification-bell.js";
import { RouteBreadcrumb } from "./route-breadcrumb.js";
import { SearchTrigger } from "./search-trigger.js";
import { useWideViewport } from "./use-media-query.js";
import { UserMenu } from "./user-menu.js";

// shell-architecture.md §15/§17: the shell's persistent global header,
// mounted once by ChromeLayout. No explicit
// role="banner" — a top-level <header> carries that landmark implicitly.
// Below 768px the bottom navigation bar carries Search and Notifications
// (chrome-header.md "States").
export function ChromeHeader(): ReactNode {
  const wide = useWideViewport();
  return (
    <header className="flex h-(--header-height) items-center justify-between gap-4 border-border border-b bg-bg px-4">
      <RouteBreadcrumb />
      <div className="flex items-center gap-2">
        {wide && <SearchTrigger />}
        <HelpButton />
        {wide && <NotificationBell />}
        <UserMenu />
      </div>
    </header>
  );
}
