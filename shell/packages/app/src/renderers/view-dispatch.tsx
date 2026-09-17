import type { ViewDeclaration } from "@goerp/sdk/schema";
import { parseManifestValue } from "@goerp/sdk/schema";
import { useMemo } from "react";
import * as v from "valibot";
import { CalendarViewDeclarationSchema } from "./calendar/calendar-manifest-types.js";
import { CalendarRenderer } from "./calendar/calendar-renderer.js";
import { FormRenderer } from "./form/form-renderer.js";
import { FormViewDeclarationSchema } from "./form/form-view-types.js";
import { KanbanViewDeclarationSchema } from "./kanban/kanban-manifest-types.js";
import { KanbanRenderer } from "./kanban/kanban-renderer.js";
import { ListRenderer } from "./list/list-renderer.js";
import { ListViewDeclarationSchema } from "./list/list-view-types.js";
import { PivotViewDeclarationSchema } from "./pivot/pivot-manifest-types.js";
import { PivotRenderer } from "./pivot/pivot-renderer.js";
import { TimelineViewDeclarationSchema } from "./timeline/timeline-manifest-types.js";
import { TimelineRenderer } from "./timeline/timeline-renderer.js";

const KNOWN_VIEW_TYPES = new Set(["list", "pivot", "kanban", "calendar", "timeline", "form"]);
const AnyViewDeclarationSchema = v.variant("type", [
  ListViewDeclarationSchema,
  PivotViewDeclarationSchema,
  KanbanViewDeclarationSchema,
  CalendarViewDeclarationSchema,
  TimelineViewDeclarationSchema,
  FormViewDeclarationSchema,
]);

export interface ViewDispatchProps {
  view: ViewDeclaration;
  module: string;
  recordId?: string | undefined;
  embedded?: boolean;
  baseFilter?: Record<string, string> | undefined;
  showCreateAction?: boolean;
}

// Logs and renders the same role="alert" degradation every invalid-dispatch
// case uses, so messages can't drift between call sites the way separately
// hand-written console.warn/JSX pairs would.
function degradeView(message: string) {
  console.warn(`ViewDispatch: ${message}`);
  return <p role="alert">{message}</p>;
}

// The dispatch switch form-tabs.tsx's EmbeddedView and the /_m/$ catch-all
// route's GenericRenderer both need — same view-type validation, same
// renderer switch, differing only in `embedded`/`baseFilter` (a form tab
// locks a baseFilter and is always embedded; a full-page route is neither).
export function ViewDispatch({ view, module, recordId, embedded, baseFilter, showCreateAction }: ViewDispatchProps) {
  // Memoized on `view` — re-parsing the full nested schema on every
  // unrelated re-render (a keystroke elsewhere in the form) is wasted work.
  const result = useMemo(
    () => (KNOWN_VIEW_TYPES.has(view.type) ? parseManifestValue(AnyViewDeclarationSchema, view) : null),
    [view],
  );

  if (!result) return <p>"{view.type}" view renderer isn't implemented yet.</p>;

  if (!result.success) {
    return degradeView(
      `"${view.name}" doesn't match the ${view.type} view schema (manifest-spec.md §9) — ${result.message}`,
    );
  }

  const validated = result.output;

  // manifest-spec.md forbids a "view" tab from embedding a "form" view;
  // nothing enforces that at manifest-load time, so a misconfigured
  // manifest can still reach here.
  if (embedded && validated.type === "form") {
    return degradeView(
      `"${validated.name}" can't be embedded as a "view" tab — nesting a form inside a form isn't supported.`,
    );
  }

  const recordIdProp = recordId !== undefined ? { recordId } : {};
  const rendererProps = {
    module,
    ...(embedded !== undefined ? { embedded } : {}),
    ...(baseFilter !== undefined ? { baseFilter } : {}),
    ...recordIdProp,
    ...(showCreateAction !== undefined ? { showCreateAction } : {}),
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
    case "form":
      // Not the shared rendererProps spread — FormRenderer has no use for
      // embedded/baseFilter/showCreateAction (embedded is already excluded
      // above), so it only declares module/recordId.
      return <FormRenderer view={validated} module={module} {...recordIdProp} />;
  }
}
