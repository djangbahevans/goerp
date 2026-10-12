import { apiClient } from "@goerp/sdk";
import { AuthContext } from "@goerp/sdk/auth";
import type { MetaSchema } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useContext } from "react";
import type { Draft, Property } from "./view-elements.js";

export interface ViewOverride {
  id: string;
  view_name: string;
  element: string;
  property: Property;
  value: string | boolean | number;
}

export function useOverrideSchema() {
  const auth = useContext(AuthContext);
  return useQuery({
    queryKey: ["view-overrides", auth?.tenant?.id, auth?.user?.id, "schema"],
    queryFn: ({ signal }) => apiClient.get<MetaSchema>("/_meta/schema", { signal }),
  });
}

export function useViewOverrides(viewName: string) {
  const auth = useContext(AuthContext);
  return useQuery({
    queryKey: ["view-overrides", auth?.tenant?.id, auth?.user?.id, "rows", viewName],
    enabled: viewName !== "",
    queryFn: async ({ signal }) =>
      (
        await apiClient.get<{ data: ViewOverride[] }>("/_meta/view-overrides", {
          params: { view_name: viewName },
          signal,
        })
      ).data,
  });
}

export async function saveElement(viewName: string, element: string, draft: Draft): Promise<void> {
  const companion = draft.hidden === true && draft.required === false;
  // Clearing hidden first permits a subsequent required write; hiding and
  // unrequiring together uses the API's atomic companion instead.
  const properties: Property[] = ["hidden", "required", "label", "help_text", "placeholder", "order"];
  for (const property of properties) {
    if (draft[property] === undefined || (companion && property === "required")) continue;
    await apiClient.post("/_meta/view-overrides", {
      view_name: viewName,
      element,
      property,
      value: draft[property],
      ...(companion && property === "hidden" ? { required: false } : {}),
    });
  }
}

export async function resetOverrides(rows: ViewOverride[]): Promise<void> {
  // Removing hidden before required avoids an intermediate hidden-required state.
  const ordered = [...rows].sort((a, b) => Number(b.property === "hidden") - Number(a.property === "hidden"));
  for (const row of ordered) await apiClient.delete(`/_meta/view-overrides/${encodeURIComponent(row.id)}`);
}
