import { useNavigate as useModuleNavigate } from "@goerp/sdk/nav";
import { ModuleNavigationProvider } from "@goerp/sdk/react";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useNavigate,
} from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useModuleNavigate as useRouterNavigate } from "../router/use-module-navigate.js";
import { useRecentCommands } from "./use-recent-commands.js";

function navigateTo(navigate: ReturnType<typeof useNavigate>, path: string) {
  void navigate({ to: path });
}

function Harness() {
  const navigate = useNavigate();
  const commands = useRecentCommands();
  const moduleNavigate = useModuleNavigate();
  return (
    <div>
      <button type="button" onClick={() => navigateTo(navigate, "/contacts")}>
        Go to Contacts
      </button>
      <button type="button" onClick={() => navigateTo(navigate, "/orders")}>
        Go to Orders
      </button>
      <ul>
        {commands.map((command) => (
          <li key={command.id}>
            {command.label}
            <button
              type="button"
              onClick={() => command.action({ navigate: moduleNavigate } as Parameters<typeof command.action>[0])}
            >
              Replay {command.label}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

async function renderHarness() {
  const rootRoute = createRootRoute({
    component: () => (
      <ModuleNavigationProvider navigate={useRouterNavigate()}>
        <Outlet />
      </ModuleNavigationProvider>
    ),
  });
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: Harness });
  const contactsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/contacts", component: Harness });
  const ordersRoute = createRoute({ getParentRoute: () => rootRoute, path: "/orders", component: Harness });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, contactsRoute, ordersRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return router;
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("useRecentCommands", () => {
  it("records the current page and each subsequent navigation, most recent first", async () => {
    await renderHarness();
    expect(screen.getByText("/")).toBeTruthy();

    screen.getByText("Go to Contacts").click();
    await screen.findByText("/contacts");
    const items = screen.getAllByRole("listitem").map((el) => el.firstChild?.textContent);
    expect(items).toEqual(["/contacts", "/"]);

    screen.getByText("Go to Orders").click();
    await screen.findByText("/orders");
    expect(screen.getAllByRole("listitem").map((el) => el.firstChild?.textContent)).toEqual([
      "/orders",
      "/contacts",
      "/",
    ]);
  });

  it("replays a browser path without adding the module prefix", async () => {
    const router = await renderHarness();
    screen.getByText("Go to Contacts").click();
    await screen.findByText("/contacts");
    screen.getByRole("button", { name: "Replay /" }).click();
    await screen.findByText("/");
    expect(router.state.location.pathname).toBe("/");
  });

  it("moves a revisited page back to the front instead of duplicating it", async () => {
    await renderHarness();
    screen.getByText("Go to Contacts").click();
    await screen.findByText("/contacts");
    screen.getByText("Go to Orders").click();
    await screen.findByText("/orders");

    screen.getByText("Go to Contacts").click();
    await screen.findByText("/contacts");
    expect(screen.getAllByRole("listitem").map((el) => el.firstChild?.textContent)).toEqual([
      "/contacts",
      "/orders",
      "/",
    ]);
  });
});
