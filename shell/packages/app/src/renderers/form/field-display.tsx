import { Badge, StatusDot } from "@goerp/sdk/components";
import { moduleLink } from "@goerp/sdk/nav";
import { resourceRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import type { MouseEvent, ReactNode } from "react";
import { renderCellContent } from "../list/column-renderers.js";
import type { ListColumn, Row } from "../list/list-view-types.js";
import { FieldInput, readFieldValue } from "./field-renderers.js";
import type { FormField } from "./form-view-types.js";

// The value a field shows in display mode (view-system.md §5 "Display and edit
// modes"): the list cell renderers, so a type reads the same in a list and a form.
const CELL_TYPE: Record<string, ListColumn["type"]> = {
  email: "email",
  phone: "phone",
  url: "url",
  number: "number",
  integer: "number",
  currency: "currency",
  percent: "percent",
  date: "date",
  datetime: "datetime",
  time: "time",
  country_select: "country",
};

const TEXT_TYPES = new Set(["text", "textarea", "language_select", "timezone_select", "currency_select"]);
const BOOLEAN_TYPES = new Set(["boolean", "toggle"]);
const RELATION_TYPES = new Set(["relation", "many2many"]);

function EmptyValue(): ReactNode {
  return (
    <span className="text-text-secondary">
      <span aria-hidden="true">—</span>
      <span className="sr-only">No value</span>
    </span>
  );
}

function isEmpty(value: unknown): boolean {
  return value == null || value === "" || (Array.isArray(value) && value.length === 0);
}

// A relation's display name, linking to the related record when its resource has a record view.
function RelationLink({ resource, id, display }: { resource: string | undefined; id: string; display: string }) {
  const router = useRouter({ warn: false });
  const { data: href } = useQuery({
    queryKey: ["resource-record-link", resource, id],
    enabled: resource !== undefined,
    queryFn: async () => {
      const entry = await resourceRegistry.resolve(resource as string);
      return entry.getPath ? moduleLink(entry.getPath.replace("{id}", id)) : null;
    },
    retry: false,
  });
  if (!href) return <span>{display}</span>;
  // Navigate in-app when a router is mounted, so the client cache survives the click.
  const onClick = router
    ? (event: MouseEvent<HTMLAnchorElement>) => {
        if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey) return;
        event.preventDefault();
        void router.navigate({ to: href });
      }
    : undefined;
  return (
    <a href={href} onClick={onClick}>
      {display}
    </a>
  );
}

function optionLabel(field: FormField, value: unknown): string {
  const key = String(value);
  return field.options?.find((option) => option.value === key)?.label ?? key;
}

export interface FieldDisplayProps {
  field: FormField;
  record: Row;
  resource: string;
}

export function FieldDisplay({ field, record, resource }: FieldDisplayProps): ReactNode {
  const type = field.type ?? "text";
  const value = readFieldValue(field, record);

  if (BOOLEAN_TYPES.has(type)) {
    return value ? <StatusDot color="green" label="Yes" /> : <StatusDot color="gray" label="No" />;
  }
  if (isEmpty(value) && type !== "computed_display") return <EmptyValue />;

  if (RELATION_TYPES.has(type)) {
    const related = Array.isArray(value) ? value : [value];
    return (
      <span className="flex flex-wrap gap-x-3 gap-y-1">
        {related.map((relation) => {
          const { id, display } = relation as { id: string; display: string };
          return <RelationLink key={id} resource={field.resource} id={id} display={display} />;
        })}
      </span>
    );
  }

  if ((type === "select" || type === "radio" || type === "multi_select") && !field.resource) {
    const values = Array.isArray(value) ? value : [value];
    return (
      <span className="inline-flex flex-wrap gap-1">
        {values.map((entry) => (
          <Badge key={String(entry)} label={optionLabel(field, entry)} color="gray" />
        ))}
      </span>
    );
  }

  if (type === "tags") {
    const tags = (Array.isArray(value) ? value : []) as { id?: string; name?: string }[];
    return (
      <span className="inline-flex flex-wrap gap-1">
        {tags.map((tag) => (
          <Badge key={tag.id ?? tag.name} label={tag.name ?? String(tag.id)} color="gray" />
        ))}
      </span>
    );
  }

  const cellType = CELL_TYPE[type];
  if (cellType) {
    const column: ListColumn = { field: field.field, type: cellType };
    if (field.currency_field !== undefined) column.currency_field = field.currency_field;
    if (field.format !== undefined) column.format = field.format;
    return <span className="break-words">{renderCellContent(column, record)}</span>;
  }

  if (TEXT_TYPES.has(type)) {
    return <span className="whitespace-pre-wrap break-words">{String(value)}</span>;
  }

  // Types with no display rendering of their own show as the disabled control they always have.
  return <FieldInput field={field} value={value} onChange={() => {}} record={record} resource={resource} disabled />;
}
