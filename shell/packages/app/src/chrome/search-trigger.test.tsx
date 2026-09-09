import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { onCommandPaletteOpenRequest } from "./command-palette-control.js";
import { SearchTrigger } from "./search-trigger.js";

afterEach(cleanup);

describe("SearchTrigger", () => {
  it("requests the command palette open on click", () => {
    const onOpenRequest = vi.fn();
    const unregister = onCommandPaletteOpenRequest(onOpenRequest);
    render(<SearchTrigger />);

    fireEvent.click(screen.getByRole("button"));

    expect(onOpenRequest).toHaveBeenCalledTimes(1);
    unregister();
  });

  it("includes a platform shortcut hint in its accessible name", () => {
    render(<SearchTrigger />);
    const button = screen.getByRole("button");
    expect(button.getAttribute("aria-label")).toMatch(/Search \((Cmd|Ctrl)\+K\)/);
  });

  it("falls through to navigator.userAgent when navigator.platform is an empty string, not just when it's missing", async () => {
    // isMac()'s result is computed once at module load — re-imported fresh
    // under a stubbed navigator so this test doesn't depend on load order.
    vi.stubGlobal("navigator", { platform: "", userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15)" });
    vi.resetModules();
    const { SearchTrigger: FreshSearchTrigger } = await import("./search-trigger.js");

    render(<FreshSearchTrigger />);
    expect(screen.getByRole("button").getAttribute("aria-label")).toBe("Search (Cmd+K)");

    vi.unstubAllGlobals();
    vi.resetModules();
  });
});
