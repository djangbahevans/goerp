import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { SectionCard } from "./section-card.js";

afterEach(cleanup);

describe("SectionCard", () => {
  it("renders the title and children", () => {
    render(
      <SectionCard title="Contact Information">
        <p>Fields go here</p>
      </SectionCard>,
    );
    expect(screen.getByRole("heading", { name: "Contact Information" })).toBeTruthy();
    expect(screen.getByText("Fields go here")).toBeTruthy();
  });

  it("shows no collapse toggle when collapsible is false", () => {
    render(
      <SectionCard title="Contact Information">
        <p>Fields go here</p>
      </SectionCard>,
    );
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("is expanded by default when collapsible, unless defaultCollapsed is set", () => {
    render(
      <SectionCard title="Contact Information" collapsible>
        <p>Fields go here</p>
      </SectionCard>,
    );
    expect(screen.getByText("Fields go here")).toBeTruthy();
    expect(screen.getByRole("button").getAttribute("aria-expanded")).toBe("true");
  });

  it("starts collapsed when defaultCollapsed is set", () => {
    render(
      <SectionCard title="Contact Information" collapsible defaultCollapsed>
        <p>Fields go here</p>
      </SectionCard>,
    );
    expect(screen.getByText("Fields go here").closest("[hidden]")).not.toBeNull();
    expect(screen.getByRole("button").getAttribute("aria-expanded")).toBe("false");
  });

  it("toggles collapsed state when the collapse button is clicked", () => {
    render(
      <SectionCard title="Contact Information" collapsible>
        <p>Fields go here</p>
      </SectionCard>,
    );
    fireEvent.click(screen.getByRole("button"));
    expect(screen.getByText("Fields go here").closest("[hidden]")).not.toBeNull();

    fireEvent.click(screen.getByRole("button"));
    expect(screen.getByText("Fields go here").closest("[hidden]")).toBeNull();
  });

  it("aria-controls always references a real element, even while collapsed", () => {
    render(
      <SectionCard title="Contact Information" collapsible defaultCollapsed>
        <p>Fields go here</p>
      </SectionCard>,
    );
    const controlsId = screen.getByRole("button").getAttribute("aria-controls");
    expect(controlsId).not.toBeNull();
    expect(document.getElementById(controlsId ?? "")).not.toBeNull();
  });

  it("gives the overflow: hidden content wrapper a 4px clip buffer, matching --shadow-focus's own spread", () => {
    render(
      <SectionCard title="Contact Information">
        <p>Fields go here</p>
      </SectionCard>,
    );
    // -m-1/p-1 cancel out visually (children land at the same position) but
    // push the actual overflow: hidden boundary 4px past the content box,
    // so a focused control flush against this edge (a rightmost-column
    // field, the last row) keeps its full focus ring instead of this
    // wrapper clipping it at rest.
    const wrapper = screen.getByText("Fields go here").parentElement;
    expect(wrapper?.className).toContain("-m-1");
    expect(wrapper?.className).toContain("p-1");
    expect(wrapper?.className).toContain("overflow-hidden");
  });
});
