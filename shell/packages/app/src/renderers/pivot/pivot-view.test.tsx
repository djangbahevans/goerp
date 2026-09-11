import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PivotView } from "./pivot-view.js";
import type { PivotHeaderNode, PivotValueColumn } from "./pivot-view-types.js";

afterEach(cleanup);

function withPermissions(children: ReactNode) {
  const value = createPermissionContextValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() });
  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
}

function rowHeaders(): PivotHeaderNode[] {
  return [{ key: "acme", label: "Acme Corp", accessibleLabel: "Acme Corp" }];
}
function columnHeaders(): PivotHeaderNode[] {
  return [{ key: "confirmed", label: "Confirmed", accessibleLabel: "Confirmed" }];
}
function values(): PivotValueColumn[] {
  return [{ key: "amount_total", label: "Revenue", format: "currency", currency: "USD" }];
}

describe("PivotView", () => {
  it("renders the title and grid together", () => {
    render(
      <PivotView
        title="Sales Analysis"
        rowHeaders={rowHeaders()}
        columnHeaders={columnHeaders()}
        values={values()}
        cells={[]}
      />,
    );
    expect(screen.getByText("Sales Analysis")).toBeTruthy();
    expect(screen.getByText("Acme Corp")).toBeTruthy();
  });

  it("shows a Download button and calls onDownload when allow_download is true", () => {
    const onDownload = vi.fn();
    render(
      withPermissions(
        <PivotView
          allowDownload
          onDownload={onDownload}
          rowHeaders={rowHeaders()}
          columnHeaders={columnHeaders()}
          values={values()}
          cells={[]}
        />,
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Download" }));
    expect(onDownload).toHaveBeenCalledOnce();
  });

  it("hides the Download button when allow_download is false", () => {
    render(
      <PivotView
        allowDownload={false}
        rowHeaders={rowHeaders()}
        columnHeaders={columnHeaders()}
        values={values()}
        cells={[]}
      />,
    );
    expect(screen.queryByRole("button", { name: "Download" })).toBeNull();
  });
});
