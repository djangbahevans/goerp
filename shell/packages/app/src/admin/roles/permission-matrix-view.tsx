import { Checkbox } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import type { MatrixSection } from "./permission-matrix.js";

export interface PermissionMatrixViewProps {
  sections: MatrixSection[];
  selected: ReadonlySet<string>;
  readOnly: boolean;
  onToggle: (name: string, checked: boolean) => void;
}

// shell-ux.md §5.2 "Permission matrix": one fieldset per module category,
// its permissions in module:resource groups.
export function PermissionMatrixView({ sections, selected, readOnly, onToggle }: PermissionMatrixViewProps): ReactNode {
  if (sections.length === 0) {
    return <p className="text-sm text-text-secondary">No enabled module declares any permissions.</p>;
  }
  return (
    <div className="flex flex-col gap-6">
      {sections.map((section) => (
        <fieldset key={section.title} className="flex flex-col gap-3">
          <legend className="mb-2 font-medium text-sm text-text-secondary">{section.title}</legend>
          {section.groups.map((group) => (
            <div key={group.key} className="flex flex-col gap-2 border-border border-s-2 ps-3">
              {group.permissions.map((permission) => (
                <Checkbox
                  key={permission.name}
                  checked={selected.has(permission.name)}
                  onChange={(checked) => onToggle(permission.name, checked)}
                  disabled={readOnly}
                  label={<span className="font-mono">{permission.name}</span>}
                  description={permission.description ?? "Not declared by any enabled module."}
                />
              ))}
            </div>
          ))}
        </fieldset>
      ))}
    </div>
  );
}
