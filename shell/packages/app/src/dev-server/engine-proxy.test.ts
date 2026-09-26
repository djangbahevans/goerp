import type { IncomingMessage, ServerResponse } from "node:http";
import type { ProxyOptions } from "vite";
import { describe, expect, it } from "vitest";
import { engineProxy } from "./engine-proxy.js";

const proxy = engineProxy("http://localhost:8080");

// The first entry whose key matches, as Vite's proxy middleware resolves it.
function matchingEntry(path: string): [string, ProxyOptions] | undefined {
  return Object.entries(proxy).find(([key]) =>
    key.startsWith("^") ? new RegExp(key).test(path) : path.startsWith(key),
  );
}

function bypass(path: string, accept: string): ReturnType<NonNullable<ProxyOptions["bypass"]>> {
  const entry = matchingEntry(path);
  const req = { url: path, headers: { accept } } as IncomingMessage;
  return entry?.[1].bypass?.(req, {} as ServerResponse, entry[1]);
}

describe("engineProxy", () => {
  it.each([
    "/@vite/client",
    "/@react-refresh",
    "/@fs/app/main.tsx",
    "/src/main.tsx",
    "/node_modules/.vite/deps/react.js",
    "/__open-in-editor?file=src/main.tsx",
    "/assets/index-abc123.js",
  ])("leaves Vite's own path %s to Vite", (path) => {
    expect(matchingEntry(path)).toBeUndefined();
  });

  it.each(["/auth/login", "/_meta/schema", "/_notif/count", "/crm/contacts/42"])(
    "forwards %s to the engine",
    (path) => {
      expect(matchingEntry(path)).toBeDefined();
      expect(bypass(path, "application/json")).toBeUndefined();
    },
  );

  it.each(["/", "/activities", "/settings/profile", "/_m/crm/contacts/42"])(
    "serves the app for a browser navigation to %s",
    (path) => {
      expect(bypass(path, "text/html,application/xhtml+xml")).toBe("/index.html");
    },
  );

  it("forwards a browser navigation to an engine built-in", () => {
    expect(bypass("/_reports/download/tok123", "text/html,application/xhtml+xml")).toBeUndefined();
  });

  it("proxies the realtime socket as a WebSocket", () => {
    expect(matchingEntry("/_ws")?.[1]).toMatchObject({ ws: true });
  });

  it("preserves the Host header on every entry", () => {
    for (const options of Object.values(proxy)) expect(options.changeOrigin).toBe(false);
  });
});
