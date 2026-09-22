import { componentRegistry } from "@goerp/sdk/schema";
import { cleanup, render, screen } from "@testing-library/react";
import { lazy } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CustomViewRenderer } from "./custom-view-renderer.js";
import type { CustomViewDeclaration } from "./custom-view-types.js";

afterEach(cleanup);

function customView(component: string): CustomViewDeclaration {
  return { name: "dashboard", type: "custom", resource: "sales.order", label: "Dashboard", component };
}

describe("CustomViewRenderer", () => {
  it("renders the registered component as the page body", () => {
    componentRegistry.register("SalesDashboard", () => <p>Dashboard content</p>);
    render(<CustomViewRenderer view={customView("SalesDashboard")} module="sales" />);
    expect(screen.getByText("Dashboard content")).toBeTruthy();
    componentRegistry.unregister("SalesDashboard");
  });

  it("forwards module/recordId/embedded/baseFilter as props, same as every other embeddable renderer type (view-system.md §5's dashboard-tab example)", () => {
    componentRegistry.register(
      "SalesDashboardProps",
      (props: { module: string; recordId?: string; embedded?: boolean; baseFilter?: Record<string, string> }) => (
        <p>{JSON.stringify(props)}</p>
      ),
    );
    render(
      <CustomViewRenderer
        view={customView("SalesDashboardProps")}
        module="accounting"
        recordId="01j"
        embedded={true}
        baseFilter={{ contact_id: "01j" }}
      />,
    );
    const props = JSON.parse(screen.getByText(/module/).textContent ?? "{}");
    expect(props).toEqual({ module: "accounting", recordId: "01j", embedded: true, baseFilter: { contact_id: "01j" } });
    componentRegistry.unregister("SalesDashboardProps");
  });

  it("shows an alert naming the view and component when the component isn't registered", () => {
    render(<CustomViewRenderer view={customView("DoesNotExist")} module="sales" />);
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("DoesNotExist");
    expect(alert.textContent).toContain("dashboard");
  });

  it("shows the Skeleton fallback until a lazily-registered component's chunk resolves", async () => {
    let resolveChunk!: () => void;
    const chunkLoaded = new Promise<void>((resolve) => {
      resolveChunk = resolve;
    });
    const LazyDashboard = lazy(async () => {
      await chunkLoaded;
      return { default: () => <p>Loaded dashboard</p> };
    });
    componentRegistry.register("LazyDashboard", LazyDashboard);

    render(<CustomViewRenderer view={customView("LazyDashboard")} module="sales" />);
    expect(document.querySelector('[data-skeleton="lines"]')).toBeTruthy();
    expect(screen.queryByText("Loaded dashboard")).toBeNull();

    resolveChunk();
    expect(await screen.findByText("Loaded dashboard")).toBeTruthy();

    componentRegistry.unregister("LazyDashboard");
  });

  it("contains a render error in an alert instead of blanking the page", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    function Throws(): never {
      throw new Error("boom");
    }
    componentRegistry.register("ThrowingDashboard", Throws);

    render(<CustomViewRenderer view={customView("ThrowingDashboard")} module="sales" />);
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("dashboard");
    expect(alert.textContent).toContain("boom");

    componentRegistry.unregister("ThrowingDashboard");
    consoleError.mockRestore();
  });
});
