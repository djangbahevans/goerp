import type { ModuleDefinition } from "@goerp/sdk";
import { registerModule } from "./register-module.js";

function hexEncode(bytes: ArrayBuffer): string {
  return Array.from(new Uint8Array(bytes))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

async function defaultImporter(bytes: ArrayBuffer): Promise<unknown> {
  const blob = new Blob([bytes], { type: "text/javascript" });
  const blobUrl = URL.createObjectURL(blob);

  try {
    // The shell never uses a bare `import(bundleUrl)` — native dynamic
    // import() has no equivalent of <script integrity>, so nothing would
    // stop a compromised CDN, a MITM'd connection, or a registry mirror
    // from serving different bytes than what was signed. The blob URL
    // here wraps only bytes already verified against bundle_sha256.
    return await import(/* @vite-ignore */ blobUrl);
  } finally {
    URL.revokeObjectURL(blobUrl);
  }
}

export interface LoadVerifiedModuleOptions {
  importer?: (bytes: ArrayBuffer) => Promise<unknown>;
}

/**
 * Fetches a module frontend bundle, verifies its SHA-256 against the
 * manifest's declared frontend.bundle_sha256, and only then imports it.
 * A hash mismatch throws before the importer is ever called.
 */
export async function loadVerifiedModule(
  bundleUrl: string,
  expectedSha256: string,
  options?: LoadVerifiedModuleOptions,
): Promise<unknown> {
  const response = await fetch(bundleUrl);
  if (!response.ok) {
    throw new Error(`failed to fetch module bundle ${bundleUrl}: ${response.status} ${response.statusText}`);
  }

  const bytes = await response.arrayBuffer();
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  const actualHex = hexEncode(digest);
  const expectedHex = expectedSha256.replace(/^sha256:/, "");

  if (actualHex !== expectedHex) {
    throw new Error(
      `module bundle ${bundleUrl} failed integrity verification: expected sha256:${expectedHex}, got sha256:${actualHex}`,
    );
  }

  const importer = options?.importer ?? defaultImporter;
  return importer(bytes);
}

// Deduplicates concurrent/repeat loads of the same module bundle for the
// process lifetime — the catch-all route's own loader (goerp#671) calls
// this once per navigation, and a module with no custom frontend (bundleUrl
// null, the documented valid case until goerp#588 ships real bundle values)
// has nothing to load: generic renderers cover the whole module in that
// case. Keyed by moduleName+bundleUrl+bundleSha256, not moduleName alone —
// a hot-reloaded module (goerp#671's own schema.updated wiring) gets a new
// bundle_url/bundle_sha256, and a stale bundle cached under the bare module
// name would otherwise never be replaced. A rejected load clears its own
// entry rather than sticking forever, so a transient fetch failure doesn't
// permanently wedge every later navigation to the same module.
const loaded = new Map<string, Promise<unknown>>();

export async function ensureLoaded(
  moduleName: string,
  bundleUrl: string | null,
  bundleSha256: string | null,
  options?: LoadVerifiedModuleOptions,
): Promise<unknown | null> {
  if (!bundleUrl || !bundleSha256) return null;

  const key = `${moduleName}:${bundleUrl}:${bundleSha256}`;
  const existing = loaded.get(key);
  if (existing) return existing;

  const promise = loadVerifiedModule(bundleUrl, bundleSha256, options).catch((err: unknown) => {
    loaded.delete(key);
    throw err;
  });
  loaded.set(key, promise);
  return promise;
}

function asModuleDefinition(moduleName: string, loadedModule: unknown): ModuleDefinition {
  const definition = (loadedModule as { default?: unknown } | null)?.default;
  if (!definition || typeof definition !== "object" || typeof (definition as { name?: unknown }).name !== "string") {
    throw new Error(`module bundle for "${moduleName}" has no valid defineModule() default export`);
  }
  return definition as ModuleDefinition;
}

// Commands are the one part of a ModuleDefinition registerModule() (not
// defineModule() itself) handles — see that function's own comment — so a
// hot-reloaded module's prior command batch needs its own unregister call
// here too, keyed by bare module name (not the bundle-specific key below),
// so the second registerModule() call replaces it instead of leaving two
// batches of the same commands in CommandRegistry.
const unregisterCommands = new Map<string, () => void>();

// Wraps ensureLoaded with the one registration step it deliberately leaves
// undone (module-loader.ts's own job is loading/verifying bytes, not
// registration). Keyed identically to ensureLoaded so registerModule() runs
// exactly once per distinct bundle, not once per navigation to a view in
// that module — ensureLoaded's own cache would otherwise return the same
// resolved promise on every call, but nothing stopped a second .then() from
// running registerModule() again each time without this registry's own,
// separate dedup.
const registered = new Map<string, Promise<void>>();

export async function ensureModuleRegistered(
  moduleName: string,
  bundleUrl: string | null,
  bundleSha256: string | null,
  options?: LoadVerifiedModuleOptions,
): Promise<void> {
  if (!bundleUrl || !bundleSha256) return;

  const key = `${moduleName}:${bundleUrl}:${bundleSha256}`;
  const existing = registered.get(key);
  if (existing) return existing;

  const promise = ensureLoaded(moduleName, bundleUrl, bundleSha256, options)
    .then((loadedModule) => {
      if (!loadedModule) return;
      unregisterCommands.get(moduleName)?.();
      unregisterCommands.set(moduleName, registerModule(asModuleDefinition(moduleName, loadedModule)));
    })
    .catch((err: unknown) => {
      registered.delete(key);
      throw err;
    });
  registered.set(key, promise);
  return promise;
}
