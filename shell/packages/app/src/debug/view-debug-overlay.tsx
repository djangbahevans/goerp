import { PermissionContext, type PermissionContextValue } from "@goerp/sdk/auth";
import {
  buildEmptyViewRegistry,
  type ResolvedView,
  type ViewRegistry,
  ViewRegistryContext,
  viewExtensionRegistry,
} from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { type ReactNode, useContext, useId, useState, useSyncExternalStore } from "react";
import { navigationStatus } from "../chrome/navigation-status.js";
import { useConditionEvaluator } from "../conditions/use-condition-evaluator.js";
import { conditionKey, VIEW_DEBUG_PARAM, viewDebugRecorder } from "./view-debug.js";
import { declarationElements, fieldSecurityDecision } from "./view-debug-model.js";

const MODULE_ROUTE_ID = "/_m/$";
const CELL = "px-2 py-1 align-top";
const CODE = "font-mono text-xs break-all";
const PRE =
  "max-h-64 overflow-auto rounded-control bg-surface-hover p-2 font-mono text-xs whitespace-pre-wrap break-all";
const MUTED = "text-text-secondary";

const EMPTY_REGISTRY = buildEmptyViewRegistry();

function useRegistry(): ViewRegistry {
  return useContext(ViewRegistryContext) ?? EMPTY_REGISTRY;
}

function usePermissions(): PermissionContextValue {
  const permissions = useContext(PermissionContext);
  if (!permissions) throw new Error("ViewDebugOverlay must be used within a PermissionProvider");
  return permissions;
}

function useCurrentModuleView(): ResolvedView | null {
  return useRouterState({
    select: (state) =>
      (state.matches.find((match) => match.routeId === MODULE_ROUTE_ID)?.loaderData as ResolvedView | null) ?? null,
  });
}

export function ViewDebugOverlay(): ReactNode {
  const view = useCurrentModuleView();
  const navigate = useNavigate();
  const [expanded, setExpanded] = useState(true);
  const bodyId = useId();

  const close = () => {
    void navigate({ to: ".", search: (previous) => ({ ...previous, [VIEW_DEBUG_PARAM]: undefined }) });
  };

  return (
    <aside
      aria-label="View debug"
      className="fixed right-4 bottom-[calc(var(--bottom-nav-height)+1rem)] z-(--z-overlay) flex max-h-[min(70vh,48rem)] w-[min(40rem,calc(100vw-2rem))] flex-col rounded-structural border border-border bg-surface text-sm text-text shadow-lg md:bottom-4"
    >
      <header className="flex items-center gap-2 border-border border-b px-3 py-2">
        <h2 className="flex-1 font-semibold">View debug</h2>
        <button
          type="button"
          aria-expanded={expanded}
          aria-controls={bodyId}
          onClick={() => setExpanded((open) => !open)}
          className="rounded-control px-2 py-1 hover:bg-surface-hover focus-visible:shadow-focus focus-visible:outline-none"
        >
          {expanded ? "Collapse" : "Expand"}
        </button>
        <button
          type="button"
          onClick={close}
          className="rounded-control px-2 py-1 hover:bg-surface-hover focus-visible:shadow-focus focus-visible:outline-none"
        >
          Close
        </button>
      </header>
      <div id={bodyId} hidden={!expanded} className="flex flex-col gap-1 overflow-y-auto p-2">
        <ViewSection view={view} />
        <ExtensionsSection view={view} />
        <DataCallsSection />
        <ConditionsSection />
        <FieldSecuritySection view={view} />
        <NavigationSection />
      </div>
    </aside>
  );
}

function Section({ title, count, children }: { title: string; count?: number; children: ReactNode }) {
  return (
    <details open className="rounded-control">
      <summary className="cursor-pointer rounded-control px-2 py-1 font-semibold hover:bg-surface-hover">
        {title}
        {count !== undefined && <span className={`ml-1 font-normal ${MUTED}`}>({count})</span>}
      </summary>
      <div className="px-2 pb-2">{children}</div>
    </details>
  );
}

function Empty({ children }: { children: ReactNode }) {
  return <p className={MUTED}>{children}</p>;
}

function ViewSection({ view }: { view: ResolvedView | null }) {
  if (!view) {
    return (
      <Section title="Declaration">
        <Empty>This page doesn't render a module view.</Empty>
      </Section>
    );
  }
  const declaration = view.declaration as unknown as Record<string, unknown>;
  const elements = declarationElements(declaration);
  return (
    <Section title="Declaration" count={elements.length}>
      <p>
        <span className={CODE}>
          {view.module}.{view.viewName}
        </span>{" "}
        <span className={MUTED}>
          {view.viewType} view of {view.declaration.resource || "no resource"}
          {view.recordId ? `, record ${view.recordId}` : ""}, declared in the {view.module} manifest
        </span>
      </p>
      <table className="mt-1 w-full">
        <tbody>
          {elements.map((element) => (
            <tr key={element.path}>
              <td className={`${CELL} ${CODE}`}>
                views.{view.viewName}.{element.path}
              </td>
              <td className={CELL}>{element.label}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <details className="mt-1">
        <summary className={`cursor-pointer ${MUTED}`}>Declaration JSON</summary>
        <pre className={PRE}>{JSON.stringify(view.declaration, null, 2)}</pre>
      </details>
    </Section>
  );
}

function ExtensionsSection({ view }: { view: ResolvedView | null }) {
  const extensions = useQuery({
    queryKey: ["view-debug-extensions", view?.module, view?.viewName],
    queryFn: () => (view ? viewExtensionRegistry.forTarget(view.module, view.viewName) : []),
    enabled: view !== null,
  });
  const entries = extensions.data ?? [];
  return (
    <Section title="View extensions" count={entries.length}>
      {entries.length === 0 ? (
        <Empty>{view ? "No module extends this view." : "No module view on this page."}</Empty>
      ) : (
        <table className="w-full">
          <tbody>
            {entries.map((entry) => (
              <tr key={`${entry.module}:${entry.ref.extension}`}>
                <td className={CELL}>
                  {entry.moduleDisplayName} <span className={`${CODE} ${MUTED}`}>({entry.module})</span>
                </td>
                <td className={`${CELL} ${CODE}`}>{entry.ref.extension}</td>
                <td className={CELL}>
                  {entry.definition ? (
                    [
                      entry.definition.type,
                      entry.definition.position,
                      entry.definition.target_section && `in ${entry.definition.target_section}`,
                    ]
                      .filter(Boolean)
                      .join(" · ")
                  ) : (
                    <span className="text-danger">definition not found; not applied</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Section>
  );
}

function useRecorderSnapshot() {
  return useSyncExternalStore(viewDebugRecorder.subscribe, viewDebugRecorder.getSnapshot);
}

function DataCallsSection() {
  const { exchanges } = useRecorderSnapshot();
  return (
    <Section title="Data calls" count={exchanges.length}>
      {exchanges.length === 0 ? (
        <Empty>No requests on this page yet.</Empty>
      ) : (
        <ul className="flex flex-col gap-1">
          {exchanges.map((exchange, index) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: the recorder only appends, so an index identifies one request.
            <li key={index}>
              <details>
                <summary className="cursor-pointer">
                  <span className={CODE}>
                    {exchange.method} {exchange.url}
                  </span>{" "}
                  <span className={exchange.status >= 400 ? "text-danger" : MUTED}>{exchange.status}</span>
                </summary>
                <pre className={PRE}>{JSON.stringify(exchange.body, null, 2)}</pre>
              </details>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

function ConditionsSection() {
  const { conditions } = useRecorderSnapshot();
  return (
    <Section title="Conditions" count={conditions.length}>
      {conditions.length === 0 ? (
        <Empty>No conditions evaluated on this page yet.</Empty>
      ) : (
        <table className="w-full">
          <tbody>
            {conditions.map((condition) => (
              <tr key={conditionKey(condition)}>
                <td className={CELL}>
                  {condition.scope} › {condition.location}
                  {condition.recordId && <span className={MUTED}> (record {condition.recordId})</span>}
                </td>
                <td className={`${CELL} ${CODE}`}>{condition.expression}</td>
                <td className={CELL}>
                  {condition.error ? (
                    <span className="text-danger">
                      error: {condition.error}; {condition.kind === "visible" ? "hidden" : "read-only"}
                    </span>
                  ) : (
                    `${condition.kind}: ${condition.result}`
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Section>
  );
}

function FieldSecuritySection({ view }: { view: ResolvedView | null }) {
  const registry = useRegistry();
  const permissions = usePermissions();
  const resource = view?.declaration.resource;
  const model = resource ? registry.models.get(resource) : undefined;
  if (!resource || !model) {
    return (
      <Section title="Field security">
        <Empty>{resource ? `No model named ${resource} is loaded.` : "This page has no model resource."}</Empty>
      </Section>
    );
  }
  const access = permissions.fieldAccess[resource] ?? {};
  return (
    <Section title="Field security" count={model.fields.length}>
      <table className="w-full">
        <tbody>
          {model.fields.map((field) => {
            const { decision, detail } = fieldSecurityDecision(access[field.name], permissions.pending === true);
            return (
              <tr key={field.name}>
                <td className={`${CELL} ${CODE}`}>{field.name}</td>
                <td className={`${CELL} ${decision === "shown" ? "" : "text-warning"}`}>{decision}</td>
                <td className={`${CELL} ${MUTED}`}>{detail}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </Section>
  );
}

function NavigationSection() {
  const registry = useRegistry();
  const permissions = usePermissions();
  const conditions = useConditionEvaluator("navigation");
  const tree = registry.navigationTree;

  const statusOf = (gate: Parameters<typeof navigationStatus>[0], location: string) =>
    navigationStatus(gate, {
      moduleEnabled: permissions.moduleEnabled,
      check: (permission) => permissions.check(permission),
      conditionHolds: (condition) => conditions.isVisible(condition, location),
    });

  return (
    <Section title="Navigation" count={tree.length}>
      {tree.length === 0 ? (
        <Empty>No module declares navigation.</Empty>
      ) : (
        <ul className="flex flex-col gap-1">
          {tree.map((group) => {
            const groupStatus = statusOf(group, `group "${group.label}" condition`);
            const items = group.children.map((item) => {
              const own = statusOf(
                { permission: item.permission, condition: item.condition },
                `item "${item.label}" condition`,
              );
              const status = groupStatus.shown
                ? own
                : { shown: false, reasons: [`group "${group.label}" is hidden`, ...own.reasons] };
              return { item, status };
            });
            const empty = groupStatus.shown && items.every(({ status }) => !status.shown);
            return (
              <li key={group.key}>
                <NavigationRow
                  label={group.label}
                  permission={group.permission}
                  shown={groupStatus.shown && !empty}
                  reasons={empty ? ["no visible items"] : groupStatus.reasons}
                />
                <ul className="ml-4">
                  {items.map(({ item, status }) => (
                    <li key={item.key}>
                      <NavigationRow
                        label={item.label}
                        permission={item.permission}
                        shown={status.shown}
                        reasons={status.reasons}
                      />
                    </li>
                  ))}
                </ul>
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}

function NavigationRow({
  label,
  permission,
  shown,
  reasons,
}: {
  label: string;
  permission: string | undefined;
  shown: boolean;
  reasons: string[];
}) {
  return (
    <p>
      <span className={shown ? "" : "line-through"}>{label}</span>{" "}
      <span className={shown ? MUTED : "text-warning"}>
        {shown ? `shown${permission ? `; has "${permission}"` : ""}` : `hidden: ${reasons.join("; ")}`}
      </span>
    </p>
  );
}
