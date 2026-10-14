import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ViewDispatch } from "./view-dispatch.js";

vi.mock("./list/list-renderer.js", async () => {
  const { useCurrentView } = await import("@goerp/sdk/nav");
  return {
    ListRenderer: (props: unknown) => {
      const current = useCurrentView();
      return (
        <>
          <div data-testid="list">{JSON.stringify(props)}</div>
          <output data-testid="context">{JSON.stringify({ ...current, params: current?.params.toString() })}</output>
        </>
      );
    },
  };
});
vi.mock("./pivot/pivot-renderer.js", async () => {
  const { useCurrentView } = await import("@goerp/sdk/nav");
  return {
    PivotRenderer: (props: unknown) => {
      const current = useCurrentView();
      return (
        <>
          <div data-testid="pivot">{JSON.stringify(props)}</div>
          <output data-testid="context">{JSON.stringify({ ...current, params: current?.params.toString() })}</output>
        </>
      );
    },
  };
});
vi.mock("./kanban/kanban-renderer.js", async () => {
  const { useCurrentView } = await import("@goerp/sdk/nav");
  return {
    KanbanRenderer: (props: unknown) => {
      const current = useCurrentView();
      return (
        <>
          <div data-testid="kanban">{JSON.stringify(props)}</div>
          <output data-testid="context">{JSON.stringify({ ...current, params: current?.params.toString() })}</output>
        </>
      );
    },
  };
});
vi.mock("./calendar/calendar-renderer.js", async () => {
  const { useCurrentView } = await import("@goerp/sdk/nav");
  return {
    CalendarRenderer: (props: unknown) => {
      const current = useCurrentView();
      return (
        <>
          <div data-testid="calendar">{JSON.stringify(props)}</div>
          <output data-testid="context">{JSON.stringify({ ...current, params: current?.params.toString() })}</output>
        </>
      );
    },
  };
});
vi.mock("./timeline/timeline-renderer.js", async () => {
  const { useCurrentView } = await import("@goerp/sdk/nav");
  return {
    TimelineRenderer: (props: unknown) => {
      const current = useCurrentView();
      return (
        <>
          <div data-testid="timeline">{JSON.stringify(props)}</div>
          <output data-testid="context">{JSON.stringify({ ...current, params: current?.params.toString() })}</output>
        </>
      );
    },
  };
});
vi.mock("./form/form-renderer.js", async () => {
  const { useCurrentView } = await import("@goerp/sdk/nav");
  return {
    FormRenderer: (props: unknown) => {
      const current = useCurrentView();
      return (
        <>
          <div data-testid="form">{JSON.stringify(props)}</div>
          <output data-testid="context">{JSON.stringify({ ...current, params: current?.params.toString() })}</output>
        </>
      );
    },
  };
});
vi.mock("./custom/custom-view-renderer.js", async () => {
  const { useCurrentView } = await import("@goerp/sdk/nav");
  return {
    CustomViewRenderer: (props: unknown) => {
      const current = useCurrentView();
      return (
        <>
          <div data-testid="custom">{JSON.stringify(props)}</div>
          <output data-testid="context">{JSON.stringify({ ...current, params: current?.params.toString() })}</output>
        </>
      );
    },
  };
});

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

describe("ViewDispatch — custom", () => {
  const customView = {
    name: "dashboard",
    type: "custom",
    resource: "sales.order",
    label: "Dashboard",
    component: "SalesDashboard",
  };

  it("dispatches a custom view to CustomViewRenderer with the validated declaration", () => {
    render(<ViewDispatch view={customView} module="sales" />);
    const props = JSON.parse(screen.getByTestId("custom").textContent ?? "{}");
    expect(props.view.component).toBe("SalesDashboard");
  });

  it("degrades the same way every other view type does when a custom declaration is missing its component", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    render(<ViewDispatch view={{ ...customView, component: undefined }} module="sales" />);
    expect(screen.getByRole("alert").textContent).toContain("doesn't match the custom view schema");
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });
});

const declarations = [
  listView,
  formView,
  { ...listView, name: "contacts_kanban", type: "kanban", group_by: "stage", card_fields: ["name"] },
  { ...listView, name: "contacts_calendar", type: "calendar", date_field: "date", title_field: "name" },
  {
    ...listView,
    name: "contacts_timeline",
    type: "timeline",
    start_field: "start",
    end_field: "end",
    label_field: "name",
  },
  { ...listView, name: "contacts_pivot", type: "pivot", rows: [], columns: [], values: [] },
  { ...listView, name: "contacts_custom", type: "custom", component: "Custom" },
];

describe("current view context", () => {
  it.each(declarations)("provides current view fields to $type renderers", (view) => {
    const { rerender } = render(
      <ViewDispatch view={view} module="contacts" params={new URLSearchParams("tab=notes&q=one&q=two")} />,
    );
    expect(JSON.parse(screen.getByTestId("context").textContent ?? "{}")).toEqual({
      viewName: view.name,
      moduleName: "contacts",
      resource: "contacts.contact",
      recordId: null,
      embedded: false,
      params: "tab=notes&q=one&q=two",
    });
    rerender(<ViewDispatch view={view} module="contacts" recordId="c1" params={new URLSearchParams("edit=true")} />);
    expect(JSON.parse(screen.getByTestId("context").textContent ?? "{}")).toMatchObject({
      recordId: "c1",
      params: "edit=true",
    });
  });

  it.each(declarations.filter((view) => view.type !== "form"))(
    "reports an embedded $type view with its parent record and empty params",
    (view) => {
      render(
        <ViewDispatch
          view={view}
          module="contacts"
          embedded
          recordId="parent"
          params={new URLSearchParams("tab=notes")}
        />,
      );
      expect(JSON.parse(screen.getByTestId("context").textContent ?? "{}")).toMatchObject({
        viewName: view.name,
        recordId: "parent",
        embedded: true,
        params: "",
      });
    },
  );

  it("reports null for a view without a resource", () => {
    render(
      <ViewDispatch
        view={{ name: "dashboard", type: "custom", resource: "", label: "Dashboard", component: "Dashboard" }}
        module="contacts"
      />,
    );
    expect(JSON.parse(screen.getByTestId("context").textContent ?? "{}").resource).toBeNull();
  });
});
