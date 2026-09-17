import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ViewDispatch } from "./view-dispatch.js";

vi.mock("./list/list-renderer.js", () => ({
  ListRenderer: (props: unknown) => <div data-testid="list">{JSON.stringify(props)}</div>,
}));
vi.mock("./pivot/pivot-renderer.js", () => ({
  PivotRenderer: (props: unknown) => <div data-testid="pivot">{JSON.stringify(props)}</div>,
}));
vi.mock("./kanban/kanban-renderer.js", () => ({
  KanbanRenderer: (props: unknown) => <div data-testid="kanban">{JSON.stringify(props)}</div>,
}));
vi.mock("./calendar/calendar-renderer.js", () => ({
  CalendarRenderer: (props: unknown) => <div data-testid="calendar">{JSON.stringify(props)}</div>,
}));
vi.mock("./timeline/timeline-renderer.js", () => ({
  TimelineRenderer: (props: unknown) => <div data-testid="timeline">{JSON.stringify(props)}</div>,
}));
vi.mock("./form/form-renderer.js", () => ({
  FormRenderer: (props: unknown) => <div data-testid="form">{JSON.stringify(props)}</div>,
}));

afterEach(cleanup);

const formView = { name: "contacts_form", type: "form", resource: "contacts.contact", label: "Contact", sections: [] };
const listView = { name: "contacts_list", type: "list", resource: "contacts.contact", label: "Contacts" };

describe("ViewDispatch — form", () => {
  it("dispatches a form view to FormRenderer with the given recordId", () => {
    render(<ViewDispatch view={formView} module="contacts" embedded={false} recordId="01j8x000000000000000000000" />);
    const props = JSON.parse(screen.getByTestId("form").textContent ?? "{}");
    expect(props.recordId).toBe("01j8x000000000000000000000");
  });

  it("renders with no recordId at all when none is given (useFormRecord's create-mode branch)", () => {
    render(<ViewDispatch view={formView} module="contacts" embedded={false} />);
    const props = JSON.parse(screen.getByTestId("form").textContent ?? "{}");
    expect("recordId" in props).toBe(false);
  });

  it("degrades the same way every other view type does when a form declaration fails schema validation", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    render(<ViewDispatch view={{ ...formView, sections: undefined }} module="contacts" />);
    expect(screen.getByRole("alert").textContent).toContain("doesn't match the form view schema");
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });

  it("degrades instead of rendering when dispatched embedded — manifest-spec.md forbids a 'view' tab from nesting a form", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    render(<ViewDispatch view={formView} module="contacts" embedded={true} baseFilter={{ contact_id: "01j" }} />);
    expect(screen.queryByTestId("form")).toBeNull();
    expect(screen.getByRole("alert").textContent).toContain("can't be embedded");
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });
});

describe("ViewDispatch — other view types unaffected", () => {
  it("still dispatches a real record id through to a non-form view type unchanged", () => {
    render(<ViewDispatch view={listView} module="contacts" recordId="01j" />);
    const props = JSON.parse(screen.getByTestId("list").textContent ?? "{}");
    expect(props.recordId).toBe("01j");
  });

  it("still falls through to the not-implemented message for a genuinely unknown view type", () => {
    render(
      <ViewDispatch view={{ name: "x", type: "gantt", resource: "contacts.contact", label: "X" }} module="contacts" />,
    );
    expect(screen.getByText(/isn't implemented yet/)).not.toBeNull();
  });
});
