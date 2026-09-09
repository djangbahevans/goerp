import { Link, rootRouteId, useMatches } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { titleCaseWords } from "./title-case-words.js";

interface Crumb {
  key: string;
  label: string;
  pathname: string;
}

type Entry = { kind: "crumb"; crumb: Crumb } | { kind: "ellipsis"; hidden: Crumb[] };

interface RouteStaticData {
  breadcrumb?: string;
}

// Falls back to a humanized last path segment when a route declares no
// staticData.breadcrumb — no manifest-driven label source (ViewRegistry,
// goerp#636/#674) exists yet to resolve real record/view names from.
function labelFor(pathname: string, staticData: RouteStaticData | undefined): string {
  if (staticData?.breadcrumb) return staticData.breadcrumb;
  if (pathname === "/") return "Home";
  const segments = pathname.split("/").filter(Boolean);
  return titleCaseWords(segments[segments.length - 1] ?? "", /[-_]/);
}

function useCrumbs(): Crumb[] {
  const matches = useMatches();
  const crumbs: Crumb[] = [];
  for (const match of matches) {
    if (match.routeId === rootRouteId) continue;
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

// chrome-header.md's own note: "reusing Breadcrumb's exact token table —
// same visual treatment, a different, non-exported mounted instance." Not
// packages/sdk/src/components/breadcrumb.tsx itself — that component is
// documented as an in-view, non-routing drill-down with no relationship to
// the router (breadcrumb.tsx's own comment).
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
                // role="img": a bare <span> has role "generic", which
                // doesn't support aria-label (biome's
                // useAriaPropsSupportedByRole) — "img" is the standard
                // pattern for a glyph whose meaning is carried entirely by
                // its label, not its literal text.
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
              ) : (
                <Link to={entry.crumb.pathname} className="text-text-secondary hover:text-text">
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
