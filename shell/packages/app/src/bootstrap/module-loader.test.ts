import { afterEach, describe, expect, it, vi } from "vitest";
import { loadVerifiedModule } from "./module-loader";

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
