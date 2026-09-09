import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { NavBadge } from "./nav-badge.js";

afterEach(cleanup);

function renderBadge(count: number, collapsed = false) {
  const queryClient = new QueryClient();
  const client = { get: async <T,>() => ({ count }) as T };
  render(
    <QueryClientProvider client={queryClient}>
      <NavBadge route="/sales/orders/pending-count" client={client} collapsed={collapsed} />
    </QueryClientProvider>,
  );
}

describe("NavBadge", () => {
  it("renders nothing when the count is zero", async () => {
    renderBadge(0);
    await waitFor(() => expect(screen.queryByText("0")).toBeNull());
  });

  it("renders the raw count under the cap", async () => {
    renderBadge(7);
    expect(await screen.findByText("7")).toBeTruthy();
  });

  it("caps display at 99+ above the cap", async () => {
    renderBadge(140);
    expect(await screen.findByText("99+")).toBeTruthy();
  });

  it("renders as a small absolutely-positioned corner overlay when collapsed", async () => {
    renderBadge(3, true);
    const badge = await screen.findByText("3");
    expect(badge.style.position).toBe("absolute");
  });
});
