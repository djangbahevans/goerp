import type { Row } from "../list/list-view-types.js";
import type { FormSidebar } from "./form-view-types.js";

export interface FormSidebarRendererProps {
  sidebar: FormSidebar;
  record: Row;
}

// Read-only display — FormSidebar has no per-field rendering hints.
export function FormSidebarRenderer({ sidebar, record }: FormSidebarRendererProps) {
  return (
    <aside style={sidebar.width ? { width: sidebar.width } : undefined}>
      {sidebar.sections.map((section, i) => (
        <div key={section.label ?? i}>
          {section.label && <h4>{section.label}</h4>}
          <dl>
            {section.fields.map((field) => (
              <div key={field}>
                <dt>{field}</dt>
                <dd>{record[field] == null ? "" : String(record[field])}</dd>
              </div>
            ))}
          </dl>
        </div>
      ))}
    </aside>
  );
}
