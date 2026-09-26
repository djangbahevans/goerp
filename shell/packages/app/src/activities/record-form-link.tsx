import { moduleLink } from "@goerp/sdk/nav";
import { resourceMetadataRegistry, viewPathRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { RouterTextLink } from "../router/text-link.js";

// The path template of model's default form view (e.g. "/orders/{id}"), or
// null when the model has none — the same resolution a list's row_click
// uses.
async function formPathTemplate(model: string): Promise<string | null> {
  const entry = await resourceMetadataRegistry.resolve(model);
  if (!entry?.defaultFormView) return null;
  return viewPathRegistry.resolveRecord(entry.defaultFormView, entry.module);
}

// A link to the record's form view, or plain text when its model has no
// form view to open.
export function RecordFormLink({
  model,
  recordId,
  children,
}: {
  model: string;
  recordId: string;
  children: ReactNode;
}): ReactNode {
  const { data: template } = useQuery({
    queryKey: ["record-form-path", model],
    queryFn: () => formPathTemplate(model),
    staleTime: Number.POSITIVE_INFINITY,
  });
  if (!template) return <span className="text-text">{children}</span>;
  return <RouterTextLink to={moduleLink(template.replace("{id}", recordId))}>{children}</RouterTextLink>;
}
