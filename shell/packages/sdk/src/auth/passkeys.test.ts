import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { confirmPasskeyEnrollment, reverifyMFA, verifyMFA } from "./auth-client.js";
import { beginPasskeyEnrollment, requestPasskeyAssertion, supportsPasskeys } from "./passkeys.js";

const registration = {
  id: "credential",
  rawId: "AP8",
  type: "public-key",
  response: {
    clientDataJSON: "AQI",
    attestationObject: "A_8",
    transports: ["internal"],
  },
  clientExtensionResults: {},
} as RegistrationResponseJSON;
const authentication = {
  id: "credential",
  rawId: "AP8",
  type: "public-key",
  response: {
    clientDataJSON: "AQI",
    authenticatorData: "A_8",
    signature: "-_8",
  },
  clientExtensionResults: {},
} as AuthenticationResponseJSON;

class BrowserCredential {
  static parseCreationOptionsFromJSON = vi.fn((options) => ({ ...options, challenge: new Uint8Array([0, 255]) }));
  static parseRequestOptionsFromJSON = vi.fn((options) => ({ ...options, challenge: new Uint8Array([0, 255]) }));
  constructor(private json: RegistrationResponseJSON | AuthenticationResponseJSON) {}
  toJSON() {
    return this.json;
  }
}

const create = vi.fn();
const get = vi.fn();
const fetchMock = vi.fn();

function optionsResponse() {
  return new Response(JSON.stringify({ ceremony_id: "ceremony", options: { publicKey: { challenge: "AP8" } } }));
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("PublicKeyCredential", BrowserCredential);
  vi.stubGlobal("navigator", { credentials: { create, get } });
  vi.stubGlobal("fetch", fetchMock);
  fetchMock.mockImplementation(async () => optionsResponse());
  create.mockResolvedValue(new BrowserCredential(registration));
  get.mockResolvedValue(new BrowserCredential(authentication));
});

afterEach(() => vi.unstubAllGlobals());

describe("passkey ceremonies", () => {
  it("uses browser JSON conversion for enrollment and sends the name with the same ceremony", async () => {
    const signal = new AbortController().signal;
    const enrollment = await beginPasskeyEnrollment({ signal });
    expect(enrollment).toEqual({ ceremonyId: "ceremony", response: registration });
    expect(BrowserCredential.parseCreationOptionsFromJSON).toHaveBeenCalledWith({ challenge: "AP8" });
    expect(create).toHaveBeenCalledWith({ publicKey: { challenge: new Uint8Array([0, 255]) }, signal });
    expect(fetchMock).toHaveBeenCalledWith(
      "/auth/mfa/enroll/webauthn",
      expect.objectContaining({
        credentials: "include",
        signal,
        body: "{}",
      }),
    );

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ recovery_codes: ["ABCDE-FGHIJ"] })));
    expect(await confirmPasskeyEnrollment({ ...enrollment!, label: "Laptop" })).toEqual(["ABCDE-FGHIJ"]);
    expect(JSON.parse(fetchMock.mock.calls.at(-1)![1].body)).toEqual({
      ceremony_id: "ceremony",
      response: registration,
      label: "Laptop",
    });
  });

  it("binds login assertion options and verification to the supplied MFA token", async () => {
    const assertion = await requestPasskeyAssertion({ mfaToken: "token" });
    expect(assertion).toEqual({ type: "webauthn", ceremonyId: "ceremony", response: authentication });
    expect(fetchMock.mock.calls[0]![0]).toBe("/auth/mfa/webauthn/options");
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({ mfa_token: "token" });
    expect(BrowserCredential.parseRequestOptionsFromJSON).toHaveBeenCalledWith({ challenge: "AP8" });

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ password_update_recommended: true })));
    expect(await verifyMFA("token", assertion!)).toEqual({ recommended: true, deadline: null });
    expect(fetchMock.mock.calls.at(-1)![0]).toBe("/auth/mfa/verify");
    expect(JSON.parse(fetchMock.mock.calls.at(-1)![1].body)).toEqual({
      mfa_token: "token",
      type: "webauthn",
      ceremony_id: "ceremony",
      response: authentication,
    });
  });

  it("uses session options for re-verification without sending a login token", async () => {
    const assertion = await requestPasskeyAssertion();
    expect(fetchMock.mock.calls[0]![0]).toBe("/auth/mfa/reverify/webauthn/options");
    expect(fetchMock.mock.calls[0]![1].body).toBe("{}");
    await reverifyMFA(assertion!);
    expect(fetchMock.mock.calls.at(-1)![0]).toBe("/auth/mfa/reverify");
    expect(JSON.parse(fetchMock.mock.calls.at(-1)![1].body)).toEqual({
      type: "webauthn",
      ceremony_id: "ceremony",
      response: authentication,
    });
  });

  it.each(["NotAllowedError", "AbortError"])("returns null on %s without submitting a credential", async (name) => {
    create.mockRejectedValueOnce(new DOMException("cancelled", name));
    get.mockRejectedValueOnce(new DOMException("cancelled", name));
    expect(await beginPasskeyEnrollment()).toBeNull();
    expect(await requestPasskeyAssertion({ mfaToken: "token" })).toBeNull();
    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/auth/mfa/enroll/webauthn",
      "/auth/mfa/webauthn/options",
    ]);
  });

  it("returns null when the browser returns no credential", async () => {
    create.mockResolvedValueOnce(null);
    get.mockResolvedValueOnce(null);
    expect(await beginPasskeyEnrollment()).toBeNull();
    expect(await requestPasskeyAssertion()).toBeNull();
  });

  it("preserves API errors and does not launch the browser after an options failure", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: "mfa_locked", message: "locked" } }), { status: 423 }),
    );
    await expect(requestPasskeyAssertion({ mfaToken: "token" })).rejects.toMatchObject({ code: "mfa_locked" });
    expect(get).not.toHaveBeenCalled();
  });

  it("preserves unexpected browser failures", async () => {
    get.mockRejectedValueOnce(new DOMException("invalid origin", "SecurityError"));
    await expect(requestPasskeyAssertion()).rejects.toMatchObject({ name: "SecurityError" });
  });

  it("does not require a platform authenticator and refuses unavailable WebAuthn before fetching", async () => {
    expect(supportsPasskeys()).toBe(true);
    vi.stubGlobal("PublicKeyCredential", undefined);
    expect(supportsPasskeys()).toBe(false);
    await expect(beginPasskeyEnrollment()).rejects.toThrow("does not support passkeys");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
