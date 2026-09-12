import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { FormSidebarRenderer } from "./form-sidebar.js";

afterEach(() => cleanup());

describe("FormSidebarRenderer", () => {
  it("renders each section as a titled SectionCard, with each field as a label/value pair", () => {
    render(
      <FormSidebarRenderer
        sidebar={{ sections: [{ label: "Overview", fields: ["status", "owner"] }] }}
        record={{ status: "Active", owner: "Jordan Lee" }}
      />,
    );
    expect(screen.getByRole("heading", { name: "Overview" })).toBeTruthy();
    expect(screen.getByText("status")).toBeTruthy();
    expect(screen.getByText("Active")).toBeTruthy();
    expect(screen.getByText("owner")).toBeTruthy();
    expect(screen.getByText("Jordan Lee")).toBeTruthy();
  });

  it("renders a field missing from the record via Field's own empty-text fallback", () => {
    render(<FormSidebarRenderer sidebar={{ sections: [{ fields: ["missing_field"] }] }} record={{}} />);
    expect(screen.getByText("—")).toBeTruthy();
  });

  it("passes FormSidebar.width through to the Sidebar's own fixed width", () => {
    const { container } = render(<FormSidebarRenderer sidebar={{ width: 320, sections: [] }} record={{}} />);
    expect((container.querySelector("aside") as HTMLElement).style.width).toBe("320px");
  });

  it("renders no heading for an unlabeled section", () => {
    render(<FormSidebarRenderer sidebar={{ sections: [{ fields: ["status"] }] }} record={{ status: "Active" }} />);
    expect(screen.queryByRole("heading")).toBeNull();
  });
});
