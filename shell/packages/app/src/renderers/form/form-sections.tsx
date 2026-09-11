import { modelRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { renderCell } from "../list/column-renderers.js";
import { ListRenderer } from "../list/list-renderer.js";
import type { ListColumn, Row } from "../list/list-view-types.js";
import { FormFieldRow } from "./form-fields.js";
import type { FormSection } from "./form-view-types.js";

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

function FieldsSection({
  section,
  resource,
  record,
  recordId,
  onChange,
  formReadonly,
}: Omit<FormSectionRendererProps, "module">) {
  return (
    <fieldset className={`grid gap-4 ${GRID_COLS_CLASS_NAME[sectionLayoutColumns(section)]}`}>
      {section.label && <legend>{section.label}</legend>}
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
    </fieldset>
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

function InlineSubList({ rows, columns }: { rows: Row[]; columns: ListColumn[] }) {
  if (rows.length === 0) return <p>None.</p>;
  return (
    <table>
      <thead>
        <tr>
          {columns.map((c) => (
            <th scope="col" key={c.field}>
              {c.label ?? c.field}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, i) => (
          <tr key={(row.id as string | undefined) ?? i}>
            {columns.map((c) => (
              <td key={c.field}>{renderCell(c, row)}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function SubListSection({ section, resource, module, record, recordId }: FormSectionRendererProps) {
  const columns = sectionListColumns(section);
  const inlineKey = section.inline_key;
  const {
    data: target,
    isLoading,
    isError,
    error,
  } = useOne2ManyTarget(resource, section.field, inlineKey === undefined);

  if (inlineKey !== undefined) {
    const rows = Array.isArray(record[inlineKey]) ? (record[inlineKey] as Row[]) : [];
    return (
      <div>
        {section.label && <h3>{section.label}</h3>}
        <InlineSubList rows={rows} columns={columns} />
      </div>
    );
  }

  if (recordId === undefined) {
    // No parent record yet — no inverse-FK value to filter by.
    return (
      <div>
        {section.label && <h3>{section.label}</h3>}
        <p>
          Save {resource} first to manage its {section.label ?? section.field}.
        </p>
      </div>
    );
  }

  if (isLoading) return <p>Loading…</p>;
  if (isError) return <p role="alert">{error instanceof Error ? error.message : String(error)}</p>;
  if (!target) return null;

  return (
    <div>
      {section.label && <h3>{section.label}</h3>}
      <ListRenderer
        view={{
          name: `${section.field}-sub-list`,
          type: "list",
          resource: target.relatedModel,
          label: section.label ?? section.field ?? "",
          columns,
        }}
        module={module}
        embedded
        baseFilter={{ [target.inverseField]: recordId }}
        recordId={recordId}
      />
    </div>
  );
}

// `section.condition` is typed but unevaluated — always renders.
export function FormSectionRenderer(props: FormSectionRendererProps) {
  const { section } = props;
  switch (section.type ?? "fields") {
    case "fields":
    case "header":
      return <FieldsSection {...props} />;
    case "sub_list":
      return <SubListSection {...props} />;
    case "custom":
      // No module component registry exists yet.
      return <p>Custom section "{section.component}" — no component registry to resolve it from yet.</p>;
    default:
      return null;
  }
}
