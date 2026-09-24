import { useLocale } from "@goerp/sdk/i18n";
import { Outlet, useRouterState } from "@tanstack/react-router";
import { type MouseEvent, type ReactNode, useEffect, useRef } from "react";
import { ChromeBanners } from "./chrome-banners.js";
import { ChromeHeader } from "./chrome-header.js";
import { ChromeSidebar } from "./chrome-sidebar.js";

// shell-architecture.md §15. ChromeSidebar sizes itself from the sidebar
// store, so the content column is a flex-1 sibling rather than offset by a
// margin. The skip link focuses <main> directly instead of navigating to
// #main-content, which would overwrite the URL hash that form views use for
// the active tab (§6).
export function ChromeLayout(): ReactNode {
  const { direction } = useLocale();
  const mainRef = useRef<HTMLElement>(null);
  const pathname = useRouterState({ select: (s) => (s.resolvedLocation ?? s.location).pathname });
  const previousPathname = useRef(pathname);

  // Accessibility "Focus management": a route navigation moves focus to
  // <main>. Only pathname changes count — filters, sort and form tabs live in
  // search params and the hash, and must keep focus on their own control.
  useEffect(() => {
    if (previousPathname.current === pathname) return;
    previousPathname.current = pathname;
    mainRef.current?.focus({ preventScroll: true });
  }, [pathname]);

  function skipToContent(event: MouseEvent<HTMLAnchorElement>): void {
    event.preventDefault();
    mainRef.current?.focus();
  }

  return (
    <div className="flex h-screen overflow-hidden bg-bg" dir={direction}>
      {/* biome-ignore lint/a11y/useValidAnchor: a skip link is conventionally an anchor to #main-content; onClick only keeps it from rewriting the URL hash. */}
      <a
        href="#main-content"
        onClick={skipToContent}
        className="sr-only focus:not-sr-only focus:absolute focus:inset-s-2 focus:top-2 focus:z-(--z-tooltip) focus:rounded-control focus:bg-primary focus:px-4 focus:py-2 focus:text-text-inverse"
      >
        Skip to content
      </a>
      <ChromeSidebar />
      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <ChromeHeader />
        <ChromeBanners mainRef={mainRef} />
        <main ref={mainRef} id="main-content" tabIndex={-1} className="flex-1 overflow-auto focus:outline-none">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
