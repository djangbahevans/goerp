import { fetchPasskeyOptions } from "./auth-client.js";
import type { MFAPasskeyConfirmation, PasskeyEnrollment } from "./types.js";

export function supportsPasskeys(): boolean {
  return (
    typeof PublicKeyCredential !== "undefined" &&
    typeof PublicKeyCredential.parseCreationOptionsFromJSON === "function" &&
    typeof PublicKeyCredential.parseRequestOptionsFromJSON === "function" &&
    typeof PublicKeyCredential.prototype.toJSON === "function" &&
    typeof navigator !== "undefined" &&
    typeof navigator.credentials?.create === "function" &&
    typeof navigator.credentials?.get === "function"
  );
}

function cancelled(err: unknown): boolean {
  return err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "AbortError");
}

export async function beginPasskeyEnrollment({
  signal,
}: {
  signal?: AbortSignal;
} = {}): Promise<PasskeyEnrollment | null> {
  if (!supportsPasskeys()) throw new Error("This browser does not support passkeys");
  try {
    const options = await fetchPasskeyOptions<PublicKeyCredentialCreationOptionsJSON>(
      "/auth/mfa/enroll/webauthn",
      {},
      signal,
    );
    const credential = await navigator.credentials.create({
      publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(options.options.publicKey),
      ...(signal ? { signal } : {}),
    });
    if (!credential) return null;
    if (!(credential instanceof PublicKeyCredential)) throw new Error("Expected a public-key credential");
    return { ceremonyId: options.ceremony_id, response: credential.toJSON() as RegistrationResponseJSON };
  } catch (err) {
    if (cancelled(err)) return null;
    throw err;
  }
}

export async function requestPasskeyAssertion({
  mfaToken,
  signal,
}: {
  mfaToken?: string;
  signal?: AbortSignal;
} = {}): Promise<MFAPasskeyConfirmation | null> {
  if (!supportsPasskeys()) throw new Error("This browser does not support passkeys");
  try {
    const options = await fetchPasskeyOptions<PublicKeyCredentialRequestOptionsJSON>(
      mfaToken === undefined ? "/auth/mfa/reverify/webauthn/options" : "/auth/mfa/webauthn/options",
      mfaToken === undefined ? {} : { mfa_token: mfaToken },
      signal,
    );
    const credential = await navigator.credentials.get({
      publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(options.options.publicKey),
      ...(signal ? { signal } : {}),
    });
    if (!credential) return null;
    if (!(credential instanceof PublicKeyCredential)) throw new Error("Expected a public-key credential");
    return {
      type: "webauthn",
      ceremonyId: options.ceremony_id,
      response: credential.toJSON() as AuthenticationResponseJSON,
    };
  } catch (err) {
    if (cancelled(err)) return null;
    throw err;
  }
}
