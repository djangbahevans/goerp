import { moduleLink } from "@goerp/sdk/nav";
import type { ResolvedView } from "@goerp/sdk/schema";
import {
  resourceMetadataRegistry,
  ViewRegistryContext,
  viewDeclarationRegistry,
  viewPathRegistry,
} from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useContext } from "react";
import { createRecordQueryOptions } from "../renderers/form/use-form-record.js";

export interface Crumb {
  key: string;
  label: string;
  // Absent for a crumb with no page of its own.
  pathname?: string;
}

interface ResourceTrail {
  labelField: string;
  list?: { label: string; pathname: string };
}

// What a record page's trail needs from the schema: the field naming a record, and the
// resource's default list view as the crumb above it.
async function loadResourceTrail(view: ResolvedView): Promise<ResourceTrail | null> {
  const entry = await resourceMetadataRegistry.resolve(view.declaration.resource);
  if (!entry) return null;
  if (!entry.defaultListView) return { labelField: entry.labelField };
  // The default list view is declared by this module or by the one that owns the resource.
  for (const module of new Set([view.module, entry.module])) {
    const [declaration, pathname] = await Promise.all([
      viewDeclarationRegistry.resolve(entry.defaultListView, module),
      viewPathRegistry.resolve(entry.defaultListView, module),
    ]);
    if (declaration && pathname) {
      return { labelField: entry.labelField, list: { label: declaration.label, pathname: moduleLink(pathname) } };
    }
  }
  return { labelField: entry.labelField };
}

// The module crumb is dropped when the crumb after it repeats its label, so "Contacts" over "Contacts" reads once.
function withoutRepeatedModule(crumbs: Crumb[]): Crumb[] {
  const [first, second] = crumbs;
  return first?.key.startsWith("module:") && first.label.toLowerCase() === second?.label.toLowerCase()
    ? crumbs.slice(1)
    : crumbs;
}

// shell-architecture.md §17's trail for a module view: module / list / record. A record crumb
// shows the record's label once the form's own record query has it, and the view's label until then.
export function useModuleViewCrumbs(view: ResolvedView | undefined): Crumb[] | null {
  const registry = useContext(ViewRegistryContext);
  const isRecord = view?.declaration.type === "form" && view.recordId !== undefined;
  const trail = useQuery({
    queryKey: ["breadcrumb", "resource-trail", view?.module, view?.declaration.resource],
    queryFn: () => loadResourceTrail(view as ResolvedView),
    enabled: isRecord,
    staleTime: 60_000,
  });
  const record = useQuery(
    createRecordQueryOptions(view?.declaration.resource ?? "", isRecord ? view?.recordId : undefined),
  );
  if (!view) return null;

  const { declaration, module, recordId } = view;
  const crumbs: Crumb[] = [];
  const moduleLabel = registry?.getModuleDisplayName(module);
  if (moduleLabel) crumbs.push({ key: `module:${module}`, label: moduleLabel });

  if (isRecord && trail.data?.list) {
    crumbs.push({ key: `list:${trail.data.list.pathname}`, ...trail.data.list });
  }

  const labelField = trail.data?.labelField;
  const recordLabel = labelField ? record.data?.[labelField] : undefined;
  crumbs.push({
    key: `view:${declaration.name}:${recordId ?? ""}`,
    label: isRecord && typeof recordLabel === "string" && recordLabel !== "" ? recordLabel : declaration.label,
  });
  return withoutRepeatedModule(crumbs);
}
