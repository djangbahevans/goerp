import { ActionButton } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { PivotGrid } from "./pivot-grid.js";
import type { PivotViewProps } from "./pivot-view-types.js";

export function PivotView({ title, allowDownload = true, onDownload, ...gridProps }: PivotViewProps): ReactNode {
  const showDownload = allowDownload && onDownload !== undefined;
  return (
    <div className="flex flex-col gap-3">
      {(title !== undefined || showDownload) && (
        <div className="flex items-center justify-between gap-3">
          {title !== undefined && <h2 className="font-medium text-lg text-text">{title}</h2>}
          {showDownload && onDownload && (
            <ActionButton variant="secondary" icon="download" onClick={onDownload}>
              Download
            </ActionButton>
          )}
        </div>
      )}
      <PivotGrid {...gridProps} />
    </div>
  );
}
