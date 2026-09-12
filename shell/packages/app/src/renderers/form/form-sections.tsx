import { DataTable, type DataTableColumn, EmptyState, SectionCard, type SectionCardProps } from "@goerp/sdk/components";
import { modelRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { renderCell } from "../list/column-renderers.js";
import { ListRenderer } from "../list/list-renderer.js";
import type { ListColumn, Row } from "../list/list-view-types.js";
import { FormFieldRow } from "./form-fields.js";
import type { FormSection } from "./form-view-types.js";

// Shared by both card-bearing section types below, so a future change to
// how these two manifest fields map to SectionCard's props (e.g. a new
// default) can't drift between them.
function sectionCardProps(section: FormSection): Pick<SectionCardProps, "title" | "collapsible" | "defaultCollapsed"> {
  return {
    title: section.label,
    collapsible: section.collapsible ?? false,
    defaultCollapsed: section.collapsed_by_default ?? false,
  };
}

// FormSection.columns carries two shapes under the same wire key.
export function sectionLayoutColumns(section: FormSection): 1 | 2 | 3 | 4 {
  return typeof section.columns === "number" ? section.columns : 2;
}

// A lookup rather than a template literal (`grid-cols-${n}`) — Tailwind's
// source scanner needs each full class name to appear literally.
const GRID_COLS_CLASS_NAME: Record<1 | 2 | 3 | 4, string> = {
  1: "grid-cols-1",
  2: "grid-cols-2",
  3: "grid-cols-3",
  4: "grid-cols-4",
};

export function sectionListColumns(section: FormSection): ListColumn[] {
  return Array.isArray(section.columns) ? section.columns : [];
}

export interface FormSectionRendererProps {
  section: FormSection;
  resource: string;
  module: string;
  record: Row;
  recordId?: string | undefined;
  onChange: (patch: Record<string, unknown>) => void;
  formReadonly: boolean;
}

// The field grid itself — shared by both section types below.
// section-card.md: `columns` is deliberately not a SectionCard prop, since
// the grid is the form renderer's own layout concern, not the card's.
function FieldsSectionGrid({
  section,
  resource,
  record,
  recordId,
  onChange,
  formReadonly,
}: Omit<FormSectionRendererProps, "module">) {
  return (
    <div className={`grid gap-4 ${GRID_COLS_CLASS_NAME[sectionLayoutColumns(section)]}`}>
      {(section.fields ?? []).map((field) => (
        // Keyed by record identity so a field's own local state (e.g.
        // TagsInput's chip buffer) resets when the record swaps.
        <FormFieldRow
          key={`${recordId ?? "new"}-${field.field}`}
          field={field}
          resource={resource}
          record={record}
          onChange={onChange}
          formReadonly={formReadonly}
        />
      ))}
    </div>
  );
}

// section-card.md's own carve-out: a "header" section reads as a compact
// identity strip, not a named collapsible group — no SectionCard wrapper.
function HeaderSection(props: Omit<FormSectionRendererProps, "module">) {
  return <FieldsSectionGrid {...props} />;
}

function FieldsSection({ section, ...rest }: Omit<FormSectionRendererProps, "module">) {
  return (
    <SectionCard {...sectionCardProps(section)}>
      <FieldsSectionGrid section={section} {...rest} />
    </SectionCard>
  );
}

// Derives the related resource/inverse-FK field from the One2Many field
// declaration. Disabled when `inline_key` is set (rows come inline instead).
function useOne2ManyTarget(parentResource: string, fieldName: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ["form-one2many-target", parentResource, fieldName],
    queryFn: async () => {
      const model = await modelRegistry.resolve(parentResource);
      const field = model.fields.find((f) => f.name === fieldName);
      if (field?.type !== "one2many" || !field.related_model || !field.inverse_field) {
        throw new Error(
          `sub_list: "${fieldName}" isn't a one2many field with related_model/inverse_field on ${parentResource}`,
        );
      }
      return { relatedModel: field.related_model, inverseField: field.inverse_field };
    },
    enabled: enabled && fieldName !== undefined,
  });
}

// list-renderer.md's own row/header/cell treatment, reused here for a
// plain in-memory row array rather than a fetched one — DataTable (already
// exactly that markup, plus its own horizontal-scroll/empty handling)
// rather than hand-rolling a second copy of it. No sort/selection/
// pagination chrome, since a manifest-inline array has none of those.
function InlineSubList({ rows, columns, emptyLabel }: { rows: Row[]; columns: ListColumn[]; emptyLabel: string }) {
  const dataColumns: DataTableColumn<Row>[] = columns.map((c) => ({
    key: c.field,
    header: c.label ?? c.field,
    render: (row) => renderCell(c, row),
  }));
  // DataTableProps.keyExtractor only receives the row, not its index — an
  // identity-keyed lookup built once here, rather than rows.indexOf(row)
  // inside the extractor itself, keeps a large id-less array's render O(n)
  // instead of O(n²).
  const positionOf = new Map<Row, number>(rows.map((row, i) => [row, i]));
  return (
    <DataTable
      columns={dataColumns}
      data={rows}
      // Falls back to a positional key for a row with no `id` — an inline
      // array (e.g. a plain JSON field) has no guaranteed row identity the
      // way a fetched, id-keyed resource does.
      keyExtractor={(row) => (typeof row.id === "string" ? row.id : String(positionOf.get(row)))}
      emptyState={<EmptyState title={`No ${emptyLabel} yet`} />}
    />
  );
}

function SubListSection({ section, resource, module, record, recordId }: FormSectionRendererProps) {
  const columns = sectionListColumns(section);
  const inlineKey = section.inline_key;
  const label = section.label ?? section.field ?? "items";
  const {
    data: target,
    isLoading,
    isError,
    error,
  } = useOne2ManyTarget(resource, section.field, inlineKey === undefined);

  const card = (children: ReactNode) => <SectionCard {...sectionCardProps(section)}>{children}</SectionCard>;

  if (inlineKey !== undefined) {
    const rows = Array.isArray(record[inlineKey]) ? (record[inlineKey] as Row[]) : [];
    return card(<InlineSubList rows={rows} columns={columns} emptyLabel={label} />);
  }

  if (recordId === undefined) {
    // No parent record yet — no inverse-FK value to filter by.
    return card(
      <p className="text-sm text-text-secondary">
        Save {resource} first to manage its {label}.
      </p>,
    );
  }

  if (isLoading) return card(<p className="text-sm text-text-secondary">Loading…</p>);
  if (isError) {
    return card(
      <p role="alert" className="text-sm text-danger">
        {error instanceof Error ? error.message : String(error)}
      </p>,
    );
  }
  if (!target) return null;

  return card(
    <ListRenderer
      view={{
        name: `${section.field}-sub-list`,
        type: "list",
        resource: target.relatedModel,
        label,
        columns,
      }}
      module={module}
      embedded
      baseFilter={{ [target.inverseField]: recordId }}
      recordId={recordId}
    />,
  );
}

// `section.condition` is typed but unevaluated — always renders.
export function FormSectionRenderer(props: FormSectionRendererProps) {
  const { section } = props;
  switch (section.type ?? "fields") {
    case "fields":
      return <FieldsSection {...props} />;
    case "header":
      return <HeaderSection {...props} />;
    case "sub_list":
      return <SubListSection {...props} />;
    case "custom":
      // No module component registry exists yet.
      return <p>Custom section "{section.component}" — no component registry to resolve it from yet.</p>;
    default:
      return null;
  }
}
