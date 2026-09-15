import type { ViewDeclaration } from "@goerp/sdk/schema";
import { parseManifestValue } from "@goerp/sdk/schema";
import { useMemo } from "react";
import * as v from "valibot";
import { CalendarViewDeclarationSchema } from "./calendar/calendar-manifest-types.js";
import { CalendarRenderer } from "./calendar/calendar-renderer.js";
import { KanbanViewDeclarationSchema } from "./kanban/kanban-manifest-types.js";
import { KanbanRenderer } from "./kanban/kanban-renderer.js";
import { ListRenderer } from "./list/list-renderer.js";
import { ListViewDeclarationSchema } from "./list/list-view-types.js";
import { PivotViewDeclarationSchema } from "./pivot/pivot-manifest-types.js";
import { PivotRenderer } from "./pivot/pivot-renderer.js";
import { TimelineViewDeclarationSchema } from "./timeline/timeline-manifest-types.js";
import { TimelineRenderer } from "./timeline/timeline-renderer.js";

// "list", "pivot", "kanban", "calendar" and "timeline" exist today — form
// (no FormViewDeclarationSchema exists yet to validate against;
// FormRenderer has no real manifest-driven call site anywhere in the app
// yet, per #671's own AC listing only these four) falls through to the
// "not implemented yet" case below.
const KNOWN_VIEW_TYPES = new Set(["list", "pivot", "kanban", "calendar", "timeline"]);
const AnyViewDeclarationSchema = v.variant("type", [
  ListViewDeclarationSchema,
  PivotViewDeclarationSchema,
  KanbanViewDeclarationSchema,
  CalendarViewDeclarationSchema,
  TimelineViewDeclarationSchema,
]);

export interface ViewDispatchProps {
  view: ViewDeclaration;
  module: string;
  recordId?: string | undefined;
  embedded?: boolean;
  baseFilter?: Record<string, string> | undefined;
}

// The dispatch switch form-tabs.tsx's EmbeddedView and the /_m/$ catch-all
// route's GenericRenderer both need — same view-type validation, same
// renderer switch, differing only in `embedded`/`baseFilter` (a form tab
// locks a baseFilter and is always embedded; a full-page route is neither).
export function ViewDispatch({ view, module, recordId, embedded, baseFilter }: ViewDispatchProps) {
  // Memoized on `view` — re-parsing the full nested schema on every
  // unrelated re-render (a keystroke elsewhere in the form) is wasted work.
  const result = useMemo(
    () => (KNOWN_VIEW_TYPES.has(view.type) ? parseManifestValue(AnyViewDeclarationSchema, view) : null),
    [view],
  );

  if (!result) return <p>"{view.type}" view renderer isn't implemented yet.</p>;

  if (!result.success) {
    // Logged, not just rendered — an uncaught crash from the old unchecked cast would have reached error monitoring.
    console.warn(
      `ViewDispatch: "${view.name}" doesn't match the ${view.type} view schema (manifest-spec.md §9) — ${result.message}`,
    );
    return (
      <p role="alert">
        "{view.name}" doesn't match the {view.type} view schema (manifest-spec.md §9) — {result.message}
      </p>
    );
  }

  const validated = result.output;
  const rendererProps = {
    module,
    ...(embedded !== undefined ? { embedded } : {}),
    ...(baseFilter !== undefined ? { baseFilter } : {}),
    ...(recordId !== undefined ? { recordId } : {}),
  };
  switch (validated.type) {
    case "list":
      return <ListRenderer view={validated} {...rendererProps} />;
    case "pivot":
      return <PivotRenderer view={validated} {...rendererProps} />;
    case "kanban":
      return <KanbanRenderer view={validated} {...rendererProps} />;
    case "calendar":
      return <CalendarRenderer view={validated} {...rendererProps} />;
    case "timeline":
      return <TimelineRenderer view={validated} {...rendererProps} />;
  }
}
