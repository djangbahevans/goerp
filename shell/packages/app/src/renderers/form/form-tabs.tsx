import { PermissionContext } from "@goerp/sdk/auth";
import { TabPanel, Tabs } from "@goerp/sdk/components";
import {
  componentRegistry,
  filterViewByCapability,
  resourceRegistry,
  summarizeIssues,
  viewDeclarationRegistry,
  viewExtensionRegistry,
} from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useContext, useState } from "react";
import * as v from "valibot";
import { useConditionEvaluator } from "../../conditions/use-condition-evaluator.js";
import type { Row } from "../list/list-view-types.js";
import { ViewDispatch } from "../view-dispatch.js";
import { FormSectionRenderer } from "./form-sections.js";
import { type FormTab, FormTabSchema } from "./form-view-types.js";

// A "tab"-typed view extension merged into this form's own tabs
// (view-system.md §10). sourceModule is the module that DECLARED the
// extension — an unqualified tab.view resolves relative to it, not to the
// target form's own module, the same way an own tab's tab.view resolves
// relative to the form's module.
type MergedTab = FormTab & { sourceModule?: string };

// useExtensionTabs resolves the "tab"-typed extensions targeting
// `{module}.{viewName}`, already ordered dependencies-first by
// viewExtensionRegistry, into ready-to-render MergedTab entries split by
// position. An entry whose definition didn't resolve, isn't a "tab" type,
// or whose `tab` member doesn't match FormTabSchema is skipped — nothing
// renders, nothing throws (view-system.md §10).
function useExtensionTabs(module: string, viewName: string) {
  return useQuery({
    queryKey: ["form-tab-extensions", module, viewName],
    queryFn: async () => {
      const entries = await viewExtensionRegistry.forTarget(module, viewName);
      const prepend: MergedTab[] = [];
      const append: MergedTab[] = [];

      for (const entry of entries) {
        if (entry.definition?.type !== "tab") continue;

        const result = v.safeParse(FormTabSchema, entry.definition.tab);
        if (!result.success) {
          console.warn(
            `useExtensionTabs: "${entry.ref.extension}" in module "${entry.module}" declares a "tab" extension whose tab member doesn't match FormTab (manifest-spec.md §11) — ${summarizeIssues(result.issues)}`,
          );
          continue;
        }

        const merged: MergedTab = { ...result.output, sourceModule: entry.module };
        (entry.definition.position === "prepend" ? prepend : append).push(merged);
      }

      return { prepend, append };
    },
  });
}

// A literal, or a `record.{field}` reference — a tab `filter` value isn't a
// domain expression, so nothing richer is interpreted here.
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
    // filterViewByCapability applied here too, not just buildViewRegistry's
    // full-page route resolution — otherwise the same view embedded as a
    // form tab would keep a "New"/quick-create action the resource's own
    // resolved routes don't actually support, that the full-page version
    // of the identical view already had stripped.
    queryFn: async () => {
      const view = await viewDeclarationRegistry.resolve(viewRef as string, module);
      if (!view) return null;
      const resource = await resourceRegistry.resolve(view.resource).catch(() => undefined);
      filterViewByCapability(view, resource);
      return view;
    },
    enabled: viewRef !== undefined,
  });
}

// The module an unqualified tab.view/tab.component resolves relative to —
// the declaring module for an extension tab (sourceModule), the form's own
// module for a native one. Same fallback resolveViewDeclaration itself
// applies when a viewRef carries no "{module}." prefix.
function tabDeclaringModule(tab: MergedTab, formModule: string): string {
  return tab.sourceModule ?? formModule;
}

function ViewTabContent({
  tab,
  module,
  record,
  recordId,
}: {
  tab: MergedTab;
  module: string;
  record: Row;
  recordId?: string | undefined;
}) {
  const declaringModule = tabDeclaringModule(tab, module);
  const { data: view, isLoading, isError } = useTabbedView(tab.view, declaringModule);
  if (isLoading) return <p>Loading…</p>;
  if (isError || !view) return <p role="alert">"{tab.view}" doesn't resolve to a view.</p>;

  // The embedded view's own module — the view name may be cross-module
  // qualified ("hr.employees_list" embedded in a contacts form), and an
  // unqualified ref inside that resolved view must in turn resolve against
  // the view's owning module, not the target form's.
  const dotIndex = (tab.view as string).indexOf(".");
  const owningModule = dotIndex < 0 ? declaringModule : (tab.view as string).slice(0, dotIndex);

  return (
    <ViewDispatch
      view={view}
      module={owningModule}
      recordId={recordId}
      baseFilter={resolveTabFilter(tab.filter, record)}
      embedded
      {...(tab.show_create_action !== undefined ? { showCreateAction: tab.show_create_action } : {})}
    />
  );
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

// Own tab or extension tab — record is always the TARGET form's own record
// (view-system.md §10: "record is the CONTACT record (not an employee
// record)" for an hr extension tab on the contacts form), so an extension
// component never needs a different prop than a module's own component tab.
function ComponentTabContent({ tab, record }: { tab: MergedTab; record: Row }) {
  const Component = componentRegistry.tryResolve(tab.component);
  if (!Component) {
    return <div role="alert">"{tab.component}" isn't a registered component — this tab can't be shown.</div>;
  }
  return <Component record={record} />;
}

export interface FormTabsRendererProps {
  tabs: FormTab[];
  resource: string;
  module: string;
  viewName: string;
  record: Row;
  recordId?: string | undefined;
  onChange: (patch: Record<string, unknown>) => void;
  formReadonly: boolean;
}

export function FormTabsRenderer({
  tabs,
  resource,
  module,
  viewName,
  record,
  recordId,
  onChange,
  formReadonly,
}: FormTabsRendererProps) {
  const permissions = useContext(PermissionContext);
  const conditions = useConditionEvaluator(`${resource} form`);
  if (!permissions) {
    throw new Error("FormTabsRenderer must be used within a PermissionProvider");
  }

  const { data: extensionTabs } = useExtensionTabs(module, viewName);
  // view-system.md §10: "target_section: tabs, position: append places the
  // tab after the form's own tabs, prepend before them" — extension entries
  // arrive already ordered dependencies-first (viewExtensionRegistry), so
  // multiple prepends/appends land in that same order relative to each other.
  const allTabs: MergedTab[] = [...(extensionTabs?.prepend ?? []), ...tabs, ...(extensionTabs?.append ?? [])];

  // Filtered once: a restricted tab can neither show a button nor become
  // active (hiding just the button would still let a stale activeId render it).
  const visibleTabs = allTabs.filter(
    (tab) =>
      (!tab.permission || permissions.check(tab.permission)) &&
      conditions.isVisible(tab.condition, `tab "${tab.label}" condition`, record),
  );

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
          {tab.type === "component" && <ComponentTabContent tab={tab} record={record} />}
        </TabPanel>
      ))}
    </Tabs>
  );
}
