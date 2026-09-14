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
