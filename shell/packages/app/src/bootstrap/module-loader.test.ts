import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ensureLoaded, ensureModuleRegistered, loadVerifiedModule } from "./module-loader";

const { registerModuleMock } = vi.hoisted(() => ({ registerModuleMock: vi.fn(() => () => {}) }));
vi.mock("./register-module.js", () => ({ registerModule: registerModuleMock }));

// SHA-256 test vector for FIXTURE_TEXT, precomputed independently via
// `sha256sum` rather than by calling this module's own hashing code —
// a hardcoded expected value catches a hashing/encoding bug that reusing
// the implementation's own digest call could not.
const FIXTURE_TEXT = "export default { name: 'demo' };";
const FIXTURE_SHA256 = "sha256:0ee976db76e8e268ca591a76009f0dd41e39a63d537dc117cef3f7f2d2923e0b";

function canned() {
  const bytes = new TextEncoder().encode(FIXTURE_TEXT).buffer;
  return { bytes, sha256: FIXTURE_SHA256 };
}

function stubFetch(response: { ok: boolean; status?: number; statusText?: string; bytes?: ArrayBuffer }) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: response.ok,
      status: response.status ?? (response.ok ? 200 : 500),
      statusText: response.statusText ?? (response.ok ? "OK" : "Internal Server Error"),
      arrayBuffer: async () => response.bytes,
    })),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  registerModuleMock.mockClear();
});

describe("loadVerifiedModule", () => {
  it("calls the importer with the fetched bytes when the hash matches", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });

    const importer = vi.fn(async (b: ArrayBuffer) => ({ default: { loaded: b.byteLength } }));

    const result = await loadVerifiedModule("https://example.test/bundle.js", sha256, { importer });

    expect(importer).toHaveBeenCalledTimes(1);
    expect(importer).toHaveBeenCalledWith(bytes);
    expect(result).toEqual({ default: { loaded: bytes.byteLength } });
  });

  it("never calls the importer and rejects when the hash does not match", async () => {
    const { bytes } = canned();
    stubFetch({ ok: true, bytes });

    const importer = vi.fn();
    const wrongHash = `sha256:${"0".repeat(64)}`;

    await expect(loadVerifiedModule("https://example.test/bundle.js", wrongHash, { importer })).rejects.toThrow(
      /integrity verification/,
    );

    expect(importer).not.toHaveBeenCalled();
  });

  it("rejects before hashing when the fetch response is not ok", async () => {
    stubFetch({ ok: false, status: 404, statusText: "Not Found" });

    const importer = vi.fn();

    await expect(
      loadVerifiedModule("https://example.test/bundle.js", `sha256:${"0".repeat(64)}`, { importer }),
    ).rejects.toThrow(/404/);

    expect(importer).not.toHaveBeenCalled();
  });

  it("defaultImporter wraps verified bytes in an object URL and always revokes it", async () => {
    // Node's ESM loader only supports file/data/node schemes, so a real
    // blob: dynamic import() cannot succeed here — this exercises the
    // create/revoke lifecycle around that import, not the browser-only
    // import itself (see shell-architecture.md §10 for the real path).
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });

    const createSpy = vi.spyOn(URL, "createObjectURL");
    const revokeSpy = vi.spyOn(URL, "revokeObjectURL");

    await expect(loadVerifiedModule("https://example.test/bundle.js", sha256)).rejects.toThrow();

    expect(createSpy).toHaveBeenCalledTimes(1);
    expect(createSpy.mock.calls[0]?.[0]).toBeInstanceOf(Blob);
    expect(revokeSpy).toHaveBeenCalledTimes(1);
    expect(revokeSpy).toHaveBeenCalledWith(createSpy.mock.results[0]?.value);
  });
});

describe("ensureLoaded", () => {
  // Each case uses its own moduleName — ensureLoaded's dedup cache is
  // module-level (persists across tests in this file), not reset per test.
  let moduleCounter = 0;
  beforeEach(() => {
    moduleCounter += 1;
  });

  it("returns null without fetching when bundleUrl is null — the no-custom-frontend case", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);

    const result = await ensureLoaded(`mod-${moduleCounter}`, null, null);

    expect(result).toBeNull();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("loads and verifies once bundleUrl/bundleSha256 are both set", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });
    const importer = vi.fn(async (b: ArrayBuffer) => ({ default: { loaded: b.byteLength } }));

    const result = await ensureLoaded(`mod-${moduleCounter}`, "https://example.test/bundle.js", sha256, {
      importer,
    });

    expect(importer).toHaveBeenCalledTimes(1);
    expect(result).toEqual({ default: { loaded: bytes.byteLength } });
  });

  it("dedupes concurrent calls for the same module into one load", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });
    const importer = vi.fn(async () => ({ default: {} }));
    const moduleName = `mod-${moduleCounter}`;

    const [first, second] = await Promise.all([
      ensureLoaded(moduleName, "https://example.test/bundle.js", sha256, { importer }),
      ensureLoaded(moduleName, "https://example.test/bundle.js", sha256, { importer }),
    ]);

    expect(importer).toHaveBeenCalledTimes(1);
    expect(first).toBe(second);
  });

  it("a later call for an already-loaded module reuses the cached result without re-fetching", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });
    const importer = vi.fn(async () => ({ default: {} }));
    const moduleName = `mod-${moduleCounter}`;
    const fetchSpy = vi.mocked(fetch);

    await ensureLoaded(moduleName, "https://example.test/bundle.js", sha256, { importer });
    fetchSpy.mockClear();
    await ensureLoaded(moduleName, "https://example.test/bundle.js", sha256, { importer });

    expect(fetchSpy).not.toHaveBeenCalled();
    expect(importer).toHaveBeenCalledTimes(1);
  });

  it("retries after a failed load instead of caching the rejection forever", async () => {
    const { sha256 } = canned();
    const moduleName = `mod-${moduleCounter}`;
    stubFetch({ ok: false, status: 500, statusText: "Internal Server Error" });

    await expect(ensureLoaded(moduleName, "https://example.test/bundle.js", sha256)).rejects.toThrow(/500/);

    const { bytes } = canned();
    stubFetch({ ok: true, bytes });
    const importer = vi.fn(async () => ({ default: {} }));

    const result = await ensureLoaded(moduleName, "https://example.test/bundle.js", sha256, { importer });

    expect(importer).toHaveBeenCalledTimes(1);
    expect(result).toEqual({ default: {} });
  });

  it("fetches again when a hot-reloaded module reports a new bundleUrl (content-addressed, so a real reload always changes it)", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });
    const importer = vi.fn(async () => ({ default: { version: 1 } }));
    const moduleName = `mod-${moduleCounter}`;

    await ensureLoaded(moduleName, "https://example.test/bundle-v1.js", sha256, { importer });

    const newImporter = vi.fn(async () => ({ default: { version: 2 } }));
    const result = await ensureLoaded(moduleName, "https://example.test/bundle-v2.js", sha256, {
      importer: newImporter,
    });

    expect(newImporter).toHaveBeenCalledTimes(1);
    expect(result).toEqual({ default: { version: 2 } });
  });
});

describe("ensureModuleRegistered", () => {
  let moduleCounter = 0;
  beforeEach(() => {
    moduleCounter += 1;
  });

  it("resolves without registering when bundleUrl is null", async () => {
    await ensureModuleRegistered(`erm-${moduleCounter}`, null, null);
    expect(registerModuleMock).not.toHaveBeenCalled();
  });

  it("calls registerModule with the loaded bundle's default export", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });
    const definition = { name: `erm-${moduleCounter}` };
    const importer = vi.fn(async () => ({ default: definition }));

    await ensureModuleRegistered(`erm-${moduleCounter}`, "https://example.test/bundle.js", sha256, { importer });

    expect(registerModuleMock).toHaveBeenCalledWith(definition);
  });

  it("registers exactly once across repeated calls for the same bundle, not once per call", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });
    const moduleName = `erm-${moduleCounter}`;
    const importer = vi.fn(async () => ({ default: { name: moduleName } }));

    await ensureModuleRegistered(moduleName, "https://example.test/bundle.js", sha256, { importer });
    await ensureModuleRegistered(moduleName, "https://example.test/bundle.js", sha256, { importer });

    expect(registerModuleMock).toHaveBeenCalledTimes(1);
  });

  it("throws a clear error when the loaded bundle has no valid defineModule() default export", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });
    const moduleName = `erm-${moduleCounter}`;
    const importer = vi.fn(async () => ({}));

    await expect(
      ensureModuleRegistered(moduleName, "https://example.test/bundle.js", sha256, { importer }),
    ).rejects.toThrow(/no valid defineModule\(\) default export/);
    expect(registerModuleMock).not.toHaveBeenCalled();
  });

  it("throws when the default export's name isn't a string, not just when name is absent", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });
    const moduleName = `erm-${moduleCounter}`;
    const importer = vi.fn(async () => ({ default: { name: 123 } }));

    await expect(
      ensureModuleRegistered(moduleName, "https://example.test/bundle.js", sha256, { importer }),
    ).rejects.toThrow(/no valid defineModule\(\) default export/);
    expect(registerModuleMock).not.toHaveBeenCalled();
  });

  it("ignores an older hot-reload's registration if it resolves after a newer one already started", async () => {
    const { bytes, sha256 } = canned();
    const moduleName = `erm-${moduleCounter}`;

    let resolveOldFetch!: (value: { ok: true; arrayBuffer: () => Promise<ArrayBuffer> }) => void;
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(() => new Promise((resolve) => (resolveOldFetch = resolve)))
      .mockImplementationOnce(async () => ({ ok: true, arrayBuffer: async () => bytes }));
    vi.stubGlobal("fetch", fetchMock);

    const oldImporter = vi.fn(async () => ({ default: { name: moduleName, version: 1 } }));
    const newImporter = vi.fn(async () => ({ default: { name: moduleName, version: 2 } }));

    const oldPromise = ensureModuleRegistered(moduleName, "https://example.test/bundle-v1.js", sha256, {
      importer: oldImporter,
    });
    const newPromise = ensureModuleRegistered(moduleName, "https://example.test/bundle-v2.js", sha256, {
      importer: newImporter,
    });
    await newPromise;
    // The older fetch only resolves now, after the newer registration has
    // already completed — simulating the older bundle's network round trip
    // simply taking longer than the newer one's.
    resolveOldFetch({ ok: true, arrayBuffer: async () => bytes });
    await oldPromise;

    expect(registerModuleMock).toHaveBeenCalledTimes(1);
    expect(registerModuleMock).toHaveBeenCalledWith({ name: moduleName, version: 2 });
  });

  it("unregisters a module's prior command batch before registering its hot-reloaded one", async () => {
    const { bytes, sha256 } = canned();
    stubFetch({ ok: true, bytes });
    const moduleName = `erm-${moduleCounter}`;
    const oldUnregister = vi.fn();
    registerModuleMock.mockReturnValueOnce(oldUnregister);
    const importer = vi.fn(async () => ({ default: { name: moduleName, version: 1 } }));

    await ensureModuleRegistered(moduleName, "https://example.test/bundle-v1.js", sha256, { importer });
    expect(oldUnregister).not.toHaveBeenCalled();

    const newImporter = vi.fn(async () => ({ default: { name: moduleName, version: 2 } }));
    await ensureModuleRegistered(moduleName, "https://example.test/bundle-v2.js", sha256, { importer: newImporter });

    expect(oldUnregister).toHaveBeenCalledTimes(1);
    expect(registerModuleMock).toHaveBeenCalledTimes(2);
  });
});
