import { PermissionContext } from "@goerp/sdk/auth";
import { TabPanel, Tabs } from "@goerp/sdk/components";
import type { ViewDeclaration } from "@goerp/sdk/schema";
import { viewDeclarationRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useContext, useState } from "react";
import { ListRenderer } from "../list/list-renderer.js";
import type { ListViewDeclaration, Row } from "../list/list-view-types.js";
import { FormSectionRenderer } from "./form-sections.js";
import type { FormTab } from "./form-view-types.js";

// A literal, or a `record.{field}` reference — not the full (unfiled)
// domain expression language `condition` needs; a "view" tab can't
// function at all without at least this much.
export function resolveRecordExpression(expr: unknown, record: Row): unknown {
  if (typeof expr !== "string") return expr;
  const match = /^record\.(\w+)$/.exec(expr);
  return match ? record[match[1] as string] : expr;
}

function resolveTabFilter(filter: Record<string, unknown> | undefined, record: Row): Record<string, string> {
  const resolved: Record<string, string> = {};
  for (const [key, expr] of Object.entries(filter ?? {})) {
    const value = resolveRecordExpression(expr, record);
    if (value !== undefined && value !== null) resolved[key] = String(value);
  }
  return resolved;
}

function useTabbedView(viewRef: string | undefined, module: string) {
  return useQuery({
    queryKey: ["form-tab-view", module, viewRef],
    queryFn: () => viewDeclarationRegistry.resolve(viewRef as string, module),
    enabled: viewRef !== undefined,
  });
}

function ViewTabContent({
  tab,
  module,
  record,
  recordId,
}: {
  tab: FormTab;
  module: string;
  record: Row;
  recordId?: string | undefined;
}) {
  const { data: view, isLoading, isError } = useTabbedView(tab.view, module);
  if (isLoading) return <p>Loading…</p>;
  if (isError || !view) return <p role="alert">"{tab.view}" doesn't resolve to a view.</p>;

  return (
    <EmbeddedView
      view={view}
      module={module}
      record={record}
      recordId={recordId}
      baseFilter={resolveTabFilter(tab.filter, record)}
    />
  );
}

// Only "list" exists today — kanban/calendar/pivot/timeline/custom are
// separate, not-yet-built tickets (goerp#644/#645/#646/#648/#647).
function EmbeddedView({
  view,
  module,
  recordId,
  baseFilter,
}: {
  view: ViewDeclaration;
  module: string;
  record: Row;
  recordId?: string | undefined;
  baseFilter: Record<string, string>;
}) {
  switch (view.type) {
    case "list":
      return (
        <ListRenderer
          view={view as unknown as ListViewDeclaration}
          module={module}
          embedded
          baseFilter={baseFilter}
          {...(recordId !== undefined ? { recordId } : {})}
        />
      );
    default:
      return <p>"{view.type}" view renderer isn't implemented yet.</p>;
  }
}

function SubListTabContent({
  tab,
  resource,
  module,
  record,
  recordId,
}: {
  tab: FormTab;
  resource: string;
  module: string;
  record: Row;
  recordId?: string | undefined;
}) {
  // Reuses the same sub_list resolution as a FormSection of that type.
  return (
    <FormSectionRenderer
      section={{
        type: "sub_list",
        columns: tab.columns ?? [],
        ...(tab.field !== undefined ? { field: tab.field } : {}),
        ...(tab.inline_key !== undefined ? { inline_key: tab.inline_key } : {}),
      }}
      resource={resource}
      module={module}
      record={record}
      recordId={recordId}
      onChange={() => {}}
      formReadonly
    />
  );
}

function FieldsTabContent({
  tab,
  resource,
  module,
  record,
  recordId,
  onChange,
  formReadonly,
}: {
  tab: FormTab;
  resource: string;
  module: string;
  record: Row;
  recordId?: string | undefined;
  onChange: (patch: Record<string, unknown>) => void;
  formReadonly: boolean;
}) {
  return (
    // Same space-y-4 form-renderer.tsx's own top-level sections list uses
    // (section-card.md's Existing Patterns table) — without it, stacked
    // SectionCards here render border-to-border with no gap.
    <div className="space-y-4">
      {(tab.sections ?? []).map((section, i) => (
        // No stable `name` guaranteed; safe since this only reorders on reload.
        <FormSectionRenderer
          key={section.name ?? i}
          section={section}
          resource={resource}
          module={module}
          record={record}
          recordId={recordId}
          onChange={onChange}
          formReadonly={formReadonly}
        />
      ))}
    </div>
  );
}

export interface FormTabsRendererProps {
  tabs: FormTab[];
  resource: string;
  module: string;
  record: Row;
  recordId?: string | undefined;
  onChange: (patch: Record<string, unknown>) => void;
  formReadonly: boolean;
}

export function FormTabsRenderer({
  tabs,
  resource,
  module,
  record,
  recordId,
  onChange,
  formReadonly,
}: FormTabsRendererProps) {
  const permissions = useContext(PermissionContext);
  if (!permissions) {
    throw new Error("FormTabsRenderer must be used within a PermissionProvider");
  }
  // Filtered once: a restricted tab can neither show a button nor become
  // active (hiding just the button would still let a stale activeId render it).
  const visibleTabs = tabs.filter((tab) => !tab.permission || permissions.check(tab.permission));

  // No stable id on FormTab — label is what key/TabPanel id already used.
  const [activeId, setActiveId] = useState<string | undefined>(() => visibleTabs[0]?.label);
  if (visibleTabs.length === 0) return null;

  // tab.badge_count_route isn't fetched/rendered — no badge yet.
  const items = visibleTabs.map((tab) => ({ id: tab.label, label: tab.label }));
  const firstId = items[0]?.id ?? "";
  const currentActiveId = items.some((item) => item.id === activeId) ? (activeId as string) : firstId;

  return (
    <Tabs items={items} activeId={currentActiveId} onChange={setActiveId}>
      {visibleTabs.map((tab) => (
        <TabPanel key={tab.label} id={tab.label}>
          {tab.type === "sub_list" && (
            <SubListTabContent tab={tab} resource={resource} module={module} record={record} recordId={recordId} />
          )}
          {tab.type === "view" && <ViewTabContent tab={tab} module={module} record={record} recordId={recordId} />}
          {tab.type === "fields" && (
            <FieldsTabContent
              tab={tab}
              resource={resource}
              module={module}
              record={record}
              recordId={recordId}
              onChange={onChange}
              formReadonly={formReadonly}
            />
          )}
          {tab.type === "component" && (
            // No module component registry exists yet.
            <p>Custom tab "{tab.component}" — no component registry to resolve it from yet.</p>
          )}
        </TabPanel>
      ))}
    </Tabs>
  );
}
