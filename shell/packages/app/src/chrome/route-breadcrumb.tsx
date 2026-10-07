import type { ResolvedView } from "@goerp/sdk/schema";
import { Link, rootRouteId, useMatches } from "@tanstack/react-router";
import type { ReactNode } from "react";
import type { FileRouteTypes } from "../router/routeTree.gen.js";
import { titleCaseWords } from "./title-case-words.js";
import { type Crumb, useModuleViewCrumbs } from "./use-module-view-crumbs.js";

type Entry = { kind: "crumb"; crumb: Crumb } | { kind: "ellipsis"; hidden: Crumb[] };

interface RouteStaticData {
  breadcrumb?: string;
}

// Humanizes the last path segment when a route declares no staticData.breadcrumb.
function labelFor(pathname: string, staticData: RouteStaticData | undefined): string {
  if (staticData?.breadcrumb) return staticData.breadcrumb;
  if (pathname === "/") return "Home";
  const segments = pathname.split("/").filter(Boolean);
  return titleCaseWords(segments[segments.length - 1] ?? "", /[-_]/);
}

const MODULE_VIEW_ROUTE_ID: FileRouteTypes["id"] = "/_m/$";

function useCrumbs(): Crumb[] {
  const matches = useMatches();
  const moduleMatch = matches.find((match) => match.routeId === MODULE_VIEW_ROUTE_ID);
  const moduleViewCrumbs = useModuleViewCrumbs(
    (moduleMatch?.loaderData as ResolvedView | null | undefined) ?? undefined,
  );
  const crumbs: Crumb[] = [];
  for (const match of matches) {
    if (match.routeId === rootRouteId) continue;
    if (match.routeId === MODULE_VIEW_ROUTE_ID) {
      // No trail until the view resolves: its path segments are ids, not names.
      crumbs.push(...(moduleViewCrumbs ?? []));
      continue;
    }
    // A pathless (layout) route match shares its child's pathname —
    // skipped rather than rendered as a same-label duplicate crumb.
    if (crumbs.at(-1)?.pathname === match.pathname) continue;
    crumbs.push({
      key: match.id,
      label: labelFor(match.pathname, match.staticData as RouteStaticData | undefined),
      pathname: match.pathname,
    });
  }
  return crumbs;
}

// chrome-header.md: beyond 3 levels, collapse the middle into one
// non-interactive "…" between the first and last two crumbs — the hidden
// crumbs' labels stay in the DOM via the ellipsis's own aria-label rather
// than being dropped, so assistive tech still gets the full trail.
function collapse(crumbs: Crumb[]): Entry[] {
  if (crumbs.length <= 3) return crumbs.map((crumb) => ({ kind: "crumb", crumb }));
  const [first, ...rest] = crumbs as [Crumb, ...Crumb[]];
  const lastTwo = rest.slice(-2);
  const hidden = rest.slice(0, -2);
  return [
    { kind: "crumb", crumb: first },
    { kind: "ellipsis", hidden },
    ...lastTwo.map((crumb): Entry => ({ kind: "crumb", crumb })),
  ];
}

// Not the exported Breadcrumb (an in-view drill-down with no router): same tokens, a separate instance.
export function RouteBreadcrumb(): ReactNode {
  const entries = collapse(useCrumbs());

  return (
    <nav aria-label="Breadcrumb">
      <ol className="flex items-center gap-1 text-sm">
        {entries.map((entry, i) => {
          const isLast = i === entries.length - 1;
          const key = entry.kind === "ellipsis" ? "ellipsis" : entry.crumb.key;
          return (
            <li key={key} className="flex items-center gap-1">
              {i > 0 && (
                <span aria-hidden="true" className="text-text-secondary">
                  ›
                </span>
              )}
              {entry.kind === "ellipsis" ? (
                // role="img": a generic <span> cannot carry aria-label.
                <span
                  role="img"
                  className="text-text-secondary"
                  aria-label={`Hidden breadcrumb levels: ${entry.hidden.map((c) => c.label).join(", ")}`}
                >
                  …
                </span>
              ) : isLast ? (
                <span aria-current="page" className="text-text">
                  {entry.crumb.label}
                </span>
              ) : entry.crumb.pathname === undefined ? (
                <span className="text-text-secondary">{entry.crumb.label}</span>
              ) : (
                <Link
                  to={entry.crumb.pathname}
                  className="text-text-secondary hover:text-text max-md:inline-flex max-md:min-h-11 max-md:items-center"
                >
                  {entry.crumb.label}
                </Link>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
