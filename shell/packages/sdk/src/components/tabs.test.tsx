import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { TabPanel, Tabs } from "./tabs.js";

afterEach(cleanup);

describe("Tabs", () => {
  it("renders a tab button per panel and only the first panel's content", () => {
    render(
      <Tabs>
        <TabPanel label="Details">
          <p>Details content</p>
        </TabPanel>
        <TabPanel label="Activity">
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
      <Tabs>
        <TabPanel label="Details">
          <p>Details content</p>
        </TabPanel>
        <TabPanel label="Activity">
          <p>Activity content</p>
        </TabPanel>
      </Tabs>,
    );
    expect(screen.getByRole("tab", { name: "Details" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("tab", { name: "Activity" }).getAttribute("aria-selected")).toBe("false");
  });

  it("switches the rendered panel when a tab is clicked", () => {
    render(
      <Tabs>
        <TabPanel label="Details">
          <p>Details content</p>
        </TabPanel>
        <TabPanel label="Activity">
          <p>Activity content</p>
        </TabPanel>
      </Tabs>,
    );
    fireEvent.click(screen.getByRole("tab", { name: "Activity" }));
    expect(screen.getByText("Activity content")).toBeTruthy();
    expect(screen.queryByText("Details content")).toBeNull();
    expect(screen.getByRole("tab", { name: "Activity" }).getAttribute("aria-selected")).toBe("true");
  });
});
