import { Field, SectionCard, Sidebar } from "@goerp/sdk/components";
import type { Row } from "../list/list-view-types.js";
import { readRecordValue, recordValueText, useModelFields } from "../record-values.js";
import type { FormSidebar } from "./form-view-types.js";

export interface FormSidebarRendererProps {
  sidebar: FormSidebar;
  record: Row;
  resource?: string;
}

export function FormSidebarRenderer({ sidebar, record, resource }: FormSidebarRendererProps) {
  const fields = useModelFields(resource);
  return (
    <Sidebar width={sidebar.width}>
      {sidebar.sections.map((section, i) => (
        <SectionCard key={section.label ?? i} title={section.label}>
          <div className="flex flex-col gap-3">
            {section.fields.map((field) => (
              <Field key={field} label={field} value={recordValueText(readRecordValue(field, record, fields))} />
            ))}
          </div>
        </SectionCard>
      ))}
    </Sidebar>
  );
}
