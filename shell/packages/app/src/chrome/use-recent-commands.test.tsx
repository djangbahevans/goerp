import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
  useNavigate,
} from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useRecentCommands } from "./use-recent-commands.js";

// This test's router only registers "/", "/contacts", "/orders" — TanStack
// Router's useNavigate() types `to` against the app's globally-registered
// router (router/index.ts's Register augmentation), not this local one, so
// a route-path literal here would fail the strict-literal-union check.
// Routing it through a `string`-typed variable instead matches how
// production code (list-actions.tsx's `navigate({ to: moduleLink(path) })`)
// already navigates to a dynamically-computed path.
function navigateTo(navigate: ReturnType<typeof useNavigate>, path: string) {
  void navigate({ to: path });
}

function Harness() {
  const navigate = useNavigate();
  const commands = useRecentCommands();
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
          <li key={command.id}>{command.label}</li>
        ))}
      </ul>
    </div>
  );
}

async function renderHarness() {
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: Harness });
  const contactsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/contacts", component: Harness });
  const ordersRoute = createRoute({ getParentRoute: () => rootRoute, path: "/orders", component: Harness });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, contactsRoute, ordersRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
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
    const items = screen.getAllByRole("listitem").map((el) => el.textContent);
    expect(items).toEqual(["/contacts", "/"]);

    screen.getByText("Go to Orders").click();
    await screen.findByText("/orders");
    expect(screen.getAllByRole("listitem").map((el) => el.textContent)).toEqual(["/orders", "/contacts", "/"]);
  });

  it("moves a revisited page back to the front instead of duplicating it", async () => {
    await renderHarness();
    screen.getByText("Go to Contacts").click();
    await screen.findByText("/contacts");
    screen.getByText("Go to Orders").click();
    await screen.findByText("/orders");

    screen.getByText("Go to Contacts").click();
    await screen.findByText("/contacts");
    expect(screen.getAllByRole("listitem").map((el) => el.textContent)).toEqual(["/contacts", "/orders", "/"]);
  });
});
