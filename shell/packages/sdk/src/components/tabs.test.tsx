import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TabPanel, Tabs } from "./tabs.js";

afterEach(cleanup);

const items = [
  { id: "details", label: "Details" },
  { id: "activity", label: "Activity" },
];

describe("Tabs", () => {
  it("renders a tab button per item and only the active panel's content", () => {
    render(
      <Tabs items={items} activeId="details" onChange={() => {}}>
        <TabPanel id="details">
          <p>Details content</p>
        </TabPanel>
        <TabPanel id="activity">
          <p>Activity content</p>
        </TabPanel>
      </Tabs>,
    );
    expect(screen.getByRole("tab", { name: "Details" })).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Activity" })).toBeTruthy();
    expect(screen.getByText("Details content")).toBeTruthy();
    expect(screen.queryByText("Activity content")).toBeNull();
  });

  it("marks the active tab as selected", () => {
    render(
      <Tabs items={items} activeId="activity" onChange={() => {}}>
        <TabPanel id="details">
          <p>Details content</p>
        </TabPanel>
        <TabPanel id="activity">
          <p>Activity content</p>
        </TabPanel>
      </Tabs>,
    );
    expect(screen.getByRole("tab", { name: "Activity" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("tab", { name: "Details" }).getAttribute("aria-selected")).toBe("false");
  });

  it("calls onChange with the clicked tab's id, without switching itself (controlled)", () => {
    const onChange = vi.fn();
    render(
      <Tabs items={items} activeId="details" onChange={onChange}>
        <TabPanel id="details">
          <p>Details content</p>
        </TabPanel>
        <TabPanel id="activity">
          <p>Activity content</p>
        </TabPanel>
      </Tabs>,
    );
    fireEvent.click(screen.getByRole("tab", { name: "Activity" }));
    expect(onChange).toHaveBeenCalledWith("activity");
    expect(screen.getByText("Details content")).toBeTruthy();
  });

  it("renders a badge count on an item that has one", () => {
    render(
      <Tabs items={[{ id: "details", label: "Details", badge: 3 }]} activeId="details" onChange={() => {}}>
        <TabPanel id="details">
          <p>Details content</p>
        </TabPanel>
      </Tabs>,
    );
    expect(screen.getByRole("tab", { name: "Details 3" })).toBeTruthy();
  });

  it("disables a tab marked disabled, without hiding it", () => {
    render(
      <Tabs items={[{ id: "details", label: "Details", disabled: true }]} activeId="details" onChange={() => {}}>
        <TabPanel id="details">
          <p>Details content</p>
        </TabPanel>
      </Tabs>,
    );
    expect(screen.getByRole("tab", { name: "Details" }).hasAttribute("disabled")).toBe(true);
  });
});
