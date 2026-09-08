import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TabItem } from "./tabs.js";
import { TabPanel, Tabs } from "./tabs.js";

afterEach(cleanup);

const items = [
  { id: "details", label: "Details" },
  { id: "activity", label: "Activity" },
];

// Tabs is controlled — exercising roving focus end-to-end (an arrow key
// both moves focus and switches the active tab) needs a real state owner,
// not a fixed activeId that never changes across a re-render.
function ControlledTabs({ items, initialId }: { items: TabItem[]; initialId?: string }) {
  const [activeId, setActiveId] = useState(initialId ?? items[0]?.id ?? "");
  return (
    <Tabs items={items} activeId={activeId} onChange={setActiveId}>
      {items.map((item) => (
        <TabPanel key={item.id} id={item.id}>
          <p>{item.label} content</p>
        </TabPanel>
      ))}
    </Tabs>
  );
}

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

  it("marks a tab marked disabled as aria-disabled, without hiding it, and ignores a click on it", () => {
    const onChange = vi.fn();
    render(
      <Tabs
        items={[
          { id: "details", label: "Details" },
          { id: "activity", label: "Activity", disabled: true },
        ]}
        activeId="details"
        onChange={onChange}
      >
        <TabPanel id="details">
          <p>Details content</p>
        </TabPanel>
        <TabPanel id="activity">
          <p>Activity content</p>
        </TabPanel>
      </Tabs>,
    );
    const activity = screen.getByRole("tab", { name: "Activity" });
    expect(activity.getAttribute("aria-disabled")).toBe("true");
    fireEvent.click(activity);
    expect(onChange).not.toHaveBeenCalled();
  });

  // Not the native `disabled` attribute, specifically so this state doesn't
  // trap keyboard focus out of the whole tablist — see goerp#689.
  it("stays focusable even when the active tab is itself disabled", () => {
    render(
      <Tabs items={[{ id: "details", label: "Details", disabled: true }]} activeId="details" onChange={() => {}}>
        <TabPanel id="details">
          <p>Details content</p>
        </TabPanel>
      </Tabs>,
    );
    const tab = screen.getByRole("tab", { name: "Details" });
    expect(tab.tabIndex).toBe(0);
    tab.focus();
    expect(document.activeElement).toBe(tab);
  });

  // aria-disabled, unlike the native disabled attribute it replaced, does
  // not suppress :hover on its own — the hover color has to be explicitly
  // overridden back for a disabled tab.
  it("suppresses the hover text color on a disabled, unselected tab", () => {
    render(
      <Tabs
        items={[
          { id: "details", label: "Details" },
          { id: "activity", label: "Activity", disabled: true },
        ]}
        activeId="details"
        onChange={() => {}}
      >
        <TabPanel id="details">
          <p>Details content</p>
        </TabPanel>
        <TabPanel id="activity">
          <p>Activity content</p>
        </TabPanel>
      </Tabs>,
    );
    expect(screen.getByRole("tab", { name: "Activity" }).className).toContain(
      "aria-disabled:hover:text-text-secondary",
    );
  });

  describe("roving tabindex keyboard navigation (goerp#689)", () => {
    it("only the active tab is in the native Tab order", () => {
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
      expect(screen.getByRole("tab", { name: "Details" }).tabIndex).toBe(-1);
      expect(screen.getByRole("tab", { name: "Activity" }).tabIndex).toBe(0);
    });

    it("ArrowRight/ArrowLeft move focus to and activate the next/previous tab, wrapping at the ends", () => {
      const threeItems = [
        { id: "a", label: "A" },
        { id: "b", label: "B" },
        { id: "c", label: "C" },
      ];
      render(<ControlledTabs items={threeItems} />);
      const tablist = screen.getByRole("tablist");
      const a = screen.getByRole("tab", { name: "A" });
      const b = screen.getByRole("tab", { name: "B" });
      const c = screen.getByRole("tab", { name: "C" });
      // The handler only fires via bubbling from a focused tab in real
      // usage — a real user reaches the tablist by tabbing to the active
      // (tabIndex=0) tab first, so tests do the same rather than firing
      // the key on the tablist div with no descendant actually focused.
      a.focus();

      fireEvent.keyDown(tablist, { key: "ArrowRight" });
      expect(document.activeElement).toBe(b);
      expect(b.getAttribute("aria-selected")).toBe("true");
      expect(a.getAttribute("aria-selected")).toBe("false");

      fireEvent.keyDown(tablist, { key: "ArrowRight" });
      expect(document.activeElement).toBe(c);

      // Wraps past the last tab back to the first.
      fireEvent.keyDown(tablist, { key: "ArrowRight" });
      expect(document.activeElement).toBe(a);
      expect(a.getAttribute("aria-selected")).toBe("true");

      // Wraps the other direction past the first tab back to the last.
      fireEvent.keyDown(tablist, { key: "ArrowLeft" });
      expect(document.activeElement).toBe(c);
      expect(c.getAttribute("aria-selected")).toBe("true");
    });

    it("skips a disabled tab during arrow-key navigation", () => {
      const itemsWithDisabled = [
        { id: "a", label: "A" },
        { id: "b", label: "B", disabled: true },
        { id: "c", label: "C" },
      ];
      render(<ControlledTabs items={itemsWithDisabled} />);
      const tablist = screen.getByRole("tablist");
      screen.getByRole("tab", { name: "A" }).focus();
      fireEvent.keyDown(tablist, { key: "ArrowRight" });
      expect(document.activeElement).toBe(screen.getByRole("tab", { name: "C" }));
    });

    it("ArrowRight and ArrowLeft move in opposite directions even starting from a disabled, focused active tab", () => {
      const itemsWithDisabledActive = [
        { id: "a", label: "A" },
        { id: "b", label: "B", disabled: true },
        { id: "c", label: "C" },
      ];
      const { unmount } = render(<ControlledTabs items={itemsWithDisabledActive} initialId="b" />);
      screen.getByRole("tab", { name: "B" }).focus();
      fireEvent.keyDown(screen.getByRole("tablist"), { key: "ArrowRight" });
      expect(document.activeElement).toBe(screen.getByRole("tab", { name: "C" }));
      unmount();

      render(<ControlledTabs items={itemsWithDisabledActive} initialId="b" />);
      screen.getByRole("tab", { name: "B" }).focus();
      fireEvent.keyDown(screen.getByRole("tablist"), { key: "ArrowLeft" });
      expect(document.activeElement).toBe(screen.getByRole("tab", { name: "A" }));
    });

    it("moving the roving focus updates which tab is in the native Tab order", () => {
      render(<ControlledTabs items={items} />);
      const tablist = screen.getByRole("tablist");
      screen.getByRole("tab", { name: "Details" }).focus();
      fireEvent.keyDown(tablist, { key: "ArrowRight" });
      expect(screen.getByRole("tab", { name: "Details" }).tabIndex).toBe(-1);
      expect(screen.getByRole("tab", { name: "Activity" }).tabIndex).toBe(0);
    });
  });
});
