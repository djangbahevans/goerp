import type { ProxyOptions } from "vite";

// Every path except Vite's own: modules, dependencies, the client runtime,
// its /__ endpoints and vite preview's built /assets. Module API routes (e.g.
// /crm/contacts) share no common prefix, so the engine's routes can't be listed.
const ENGINE_PATHS = "^/(?!@|__|src/|node_modules/|assets/)";

// A browser navigation loads the app, except to an engine built-in under /_
// (e.g. /_reports/download/{token}); /_m/ is the app's own module-view space.
// A module's own unprefixed GET route opened in a tab is indistinguishable
// from an app page here, so it gets the app in dev.
function servesApp(path: string, accept: string | undefined): boolean {
  const engineBuiltIn = path.startsWith("/_") && !path.startsWith("/_m/");
  return !engineBuiltIn && (accept?.includes("text/html") ?? false);
}

// The Vite dev server's proxy to a local engine. Host is preserved because
// the engine resolves the tenant from it and the auth cookies are host-bound.
export function engineProxy(target: string): Record<string, ProxyOptions> {
  return {
    "/_ws": { target, ws: true, changeOrigin: false },
    [ENGINE_PATHS]: {
      target,
      changeOrigin: false,
      bypass: (req) => (servesApp(req.url ?? "", req.headers.accept) ? "/index.html" : undefined),
    },
  };
}
