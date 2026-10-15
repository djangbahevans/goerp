import { afterEach, describe, expect, it } from "vitest";
import { suppressChunkReloadWhileOffline } from "./offline-chunk-reload.js";

const KEY = "tanstack_router_reload:Failed to fetch dynamically imported module: /src/routes/x.tsx";
let restore = () => {};

afterEach(() => {
  restore();
  window.sessionStorage.clear();
  window.localStorage.clear();
});

describe("suppressChunkReloadWhileOffline", () => {
  it("reports TanStack's chunk reload as already done only while offline", () => {
    let online = true;
    restore = suppressChunkReloadWhileOffline(window.sessionStorage, { get: () => online });
    expect(window.sessionStorage.getItem(KEY)).toBeNull();

    online = false;
    expect(window.sessionStorage.getItem(KEY)).toBe("1");
    expect(window.sessionStorage.getItem("other")).toBeNull();
    expect(window.localStorage.getItem(KEY)).toBeNull();
  });

  it("restores the original getItem", () => {
    restore = suppressChunkReloadWhileOffline(window.sessionStorage, { get: () => false });
    restore();
    restore = () => {};
    expect(window.sessionStorage.getItem(KEY)).toBeNull();
  });
});
