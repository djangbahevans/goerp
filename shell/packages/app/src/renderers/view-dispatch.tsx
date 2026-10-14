import { CurrentViewProvider } from "@goerp/sdk/nav";
import type { ViewDeclaration } from "@goerp/sdk/schema";
import { parseManifestValue } from "@goerp/sdk/schema";
import { useMemo } from "react";
import * as v from "valibot";
import { CalendarViewDeclarationSchema } from "./calendar/calendar-manifest-types.js";
import { CalendarRenderer } from "./calendar/calendar-renderer.js";
import { CustomViewRenderer } from "./custom/custom-view-renderer.js";
import { CustomViewDeclarationSchema } from "./custom/custom-view-types.js";
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

export const KNOWN_VIEW_TYPES = new Set(["list", "pivot", "kanban", "calendar", "timeline", "form", "custom"]);
export const AnyViewDeclarationSchema = v.variant("type", [
  ListViewDeclarationSchema,
  PivotViewDeclarationSchema,
  KanbanViewDeclarationSchema,
  CalendarViewDeclarationSchema,
  TimelineViewDeclarationSchema,
  FormViewDeclarationSchema,
  CustomViewDeclarationSchema,
]);

export interface ViewDispatchProps {
  view: ViewDeclaration;
  module: string;
  recordId?: string | undefined;
  embedded?: boolean;
  baseFilter?: Record<string, string> | undefined;
  showCreateAction?: boolean;
  params?: URLSearchParams;
}

function degradeView(message: string) {
  console.warn(`ViewDispatch: ${message}`);
  return <p role="alert">{message}</p>;
}

export function ViewDispatch({ params, ...props }: ViewDispatchProps) {
  const { view, module, recordId, embedded } = props;
  const currentView = useMemo(
    () => ({
      viewName: view.name,
      moduleName: module,
      resource: view.resource || null,
      recordId: recordId ?? null,
      embedded: embedded ?? false,
      params: embedded ? new URLSearchParams() : (params ?? new URLSearchParams()),
    }),
    [view.name, view.resource, module, recordId, embedded, params],
  );
  return (
    <CurrentViewProvider value={currentView}>
      <DispatchedView {...props} />
    </CurrentViewProvider>
  );
}

function DispatchedView({ view, module, recordId, embedded, baseFilter, showCreateAction }: ViewDispatchProps) {
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

  // Nested forms are invalid even when a malformed declaration reaches the shell.
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
      return <FormRenderer view={validated} module={module} {...recordIdProp} />;
    case "custom":
      return <CustomViewRenderer view={validated} {...rendererProps} />;
  }
}
