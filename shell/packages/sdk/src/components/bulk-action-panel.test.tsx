import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { BulkActionPanel } from "./bulk-action-panel.js";

afterEach(cleanup);

describe("BulkActionPanel", () => {
  it("renders its children within a labeled region", () => {
    render(
      <BulkActionPanel>
        <button type="button">Add to 3 contacts</button>
      </BulkActionPanel>,
    );
    expect(screen.getByRole("region", { name: "Bulk action" })).toBeTruthy();
    expect(screen.getByText("Add to 3 contacts")).toBeTruthy();
  });
});
