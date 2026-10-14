import { cleanup, configureViewTests } from "@goerp/shell-app/testing";
import { afterEach, expect, vi } from "vitest";
import fixture from "./api/generated.testing.json";
import module from "./index";

window.matchMedia = vi.fn().mockReturnValue({
  matches: true,
  addEventListener: vi.fn(),
  removeEventListener: vi.fn(),
});
configureViewTests({ fixture, module });
const originalFetch = globalThis.fetch;
afterEach(() => {
  cleanup();
  expect(globalThis.fetch).toBe(originalFetch);
});
