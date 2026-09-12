import { Field, SectionCard, Sidebar } from "@goerp/sdk/components";
import type { Row } from "../list/list-view-types.js";
import type { FormSidebar } from "./form-view-types.js";

export interface FormSidebarRendererProps {
  sidebar: FormSidebar;
  record: Row;
}

// Read-only display — FormSidebar has no per-field rendering hints, so
// every field renders through Field's default "text" type (sidebar.md:
// Sidebar's children are "typically one or more SectionCards").
export function FormSidebarRenderer({ sidebar, record }: FormSidebarRendererProps) {
  return (
    <Sidebar width={sidebar.width}>
      {sidebar.sections.map((section, i) => (
        <SectionCard key={section.label ?? i} title={section.label}>
          <div className="flex flex-col gap-3">
            {section.fields.map((field) => (
              <Field key={field} label={field} value={record[field]} />
            ))}
          </div>
        </SectionCard>
      ))}
    </Sidebar>
  );
}
