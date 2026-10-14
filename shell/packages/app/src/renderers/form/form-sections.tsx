import {
  DataTable,
  type DataTableColumn,
  EmptyState,
  SectionCard,
  type SectionCardProps,
  Skeleton,
} from "@goerp/sdk/components";
import { modelRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useConditionEvaluator } from "../../conditions/use-condition-evaluator.js";
import { renderCell } from "../list/column-renderers.js";
import { ListRenderer } from "../list/list-renderer.js";
import type { ListColumn, Row } from "../list/list-view-types.js";
import { useVisibleColumns } from "../list/use-visible-columns.js";
import { LoadFailed } from "../shared/load-failed.js";
import { EditableSubList } from "./editable-sub-list.js";
import { FormFieldRow } from "./form-fields.js";
import type { FormSection } from "./form-view-types.js";

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
// source scanner needs each full class name to appear literally. Below 768px
// every section is one column (shell-architecture.md §23).
const GRID_COLS_CLASS_NAME: Record<1 | 2 | 3 | 4, string> = {
  1: "grid-cols-1",
  2: "grid-cols-1 md:grid-cols-2",
  3: "grid-cols-1 md:grid-cols-3",
  4: "grid-cols-1 md:grid-cols-4",
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

function useOne2ManyTarget(parentResource: string, fieldName: string | undefined) {
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
    enabled: fieldName !== undefined,
  });
}

function InlineSubList({
  rows,
  columns: declaredColumns,
  resource,
  emptyLabel,
}: {
  rows: Row[];
  columns: ListColumn[];
  resource: string;
  emptyLabel: string;
}) {
  const { columns } = useVisibleColumns({
    name: `${resource}-inline-sub-list`,
    type: "list",
    resource,
    label: emptyLabel,
    columns: declaredColumns,
  });
  const dataColumns: DataTableColumn<Row>[] = columns.map((c) => ({
    key: c.field,
    header: c.label ?? c.field,
    render: (row) => renderCell(c, row),
  }));
  // Indexing id-less rows once avoids a quadratic search in the key extractor.
  const positionOf = new Map<Row, number>(rows.map((row, i) => [row, i]));
  return (
    <DataTable
      columns={dataColumns}
      data={rows}
      keyExtractor={(row) => (typeof row.id === "string" ? row.id : String(positionOf.get(row)))}
      emptyState={<EmptyState title={`No ${emptyLabel} yet`} />}
    />
  );
}

function SubListSection({ section, resource, module, record, recordId, formReadonly }: FormSectionRendererProps) {
  const columns = sectionListColumns(section);
  const inlineKey = section.inline_key;
  const label = section.label ?? section.field ?? "items";
  const inlineEdit = section.inline_edit === true;
  const { data: target, isLoading, isFetching, isError, error, refetch } = useOne2ManyTarget(resource, section.field);

  const card = (children: ReactNode) => <SectionCard {...sectionCardProps(section)}>{children}</SectionCard>;

  // Inline rows still need the related model to enforce its field security before rendering.
  if (inlineKey !== undefined && section.field !== undefined) {
    if (isLoading || (isError && isFetching)) {
      return card(<Skeleton type="table" columns={Math.max(columns.length, 1)} />);
    }
    if (isError) {
      return card(
        <LoadFailed
          message={section.label ? `Couldn't load ${section.label}.` : "Couldn't load this list."}
          detail={error.message}
          onRetry={() => {
            void refetch();
          }}
        />,
      );
    }
    if (!target) return null;
  }

  const readOnly =
    inlineKey !== undefined ? (
      <InlineSubList
        rows={Array.isArray(record[inlineKey]) ? (record[inlineKey] as Row[]) : []}
        columns={columns}
        resource={target?.relatedModel ?? resource}
        emptyLabel={label}
      />
    ) : recordId !== undefined && target ? (
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
      />
    ) : null;

  if (inlineEdit && !formReadonly) {
    if (recordId === undefined) {
      return card(
        <p className="text-sm text-text-secondary">
          Save {resource} first to manage its {label}.
        </p>,
      );
    }
    if (isLoading) return card(<Skeleton type="table" columns={Math.max(columns.length, 1)} />);
    if (target) {
      return card(
        <EditableSubList
          section={section}
          parentResource={resource}
          parentRecord={record}
          recordId={recordId}
          target={target}
          columns={columns}
          label={label}
          formReadonly={formReadonly}
          readOnlyFallback={readOnly}
        />,
      );
    }
    if (inlineKey !== undefined) return card(readOnly);
  }

  if (inlineKey !== undefined) {
    return card(readOnly);
  }

  if (recordId === undefined) {
    return card(
      <p className="text-sm text-text-secondary">
        Save {resource} first to manage its {label}.
      </p>,
    );
  }

  if (isLoading || (isError && isFetching))
    return card(<Skeleton type="table" columns={Math.max(columns.length, 1)} />);
  if (isError) {
    return card(
      <LoadFailed
        message={section.label ? `Couldn't load ${section.label}.` : "Couldn't load this list."}
        detail={error.message}
        onRetry={() => {
          void refetch();
        }}
      />,
    );
  }
  if (!target) return null;

  return card(readOnly);
}

export function FormSectionRenderer(props: FormSectionRendererProps) {
  const { section, resource, record } = props;
  const conditions = useConditionEvaluator(`${resource} form`);
  if (
    !conditions.isVisible(
      section.condition,
      `section "${section.name ?? section.label ?? section.field}" condition`,
      record,
    )
  ) {
    return null;
  }
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
