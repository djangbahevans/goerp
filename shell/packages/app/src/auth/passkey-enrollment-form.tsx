import {
  beginPasskeyEnrollment,
  confirmPasskeyEnrollment,
  type MFACodeConfirmation,
  type MFAVerification,
  type PasskeyEnrollment,
  type PasskeyEnrollmentConfirmation,
  requestPasskeyAssertion,
  reverifyMFA,
} from "@goerp/sdk/auth";
import { Button, FieldWrapper, TextInput } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { type ReactNode, type SubmitEvent, useEffect, useRef, useState } from "react";
import { MFACodeForm } from "../settings/mfa-code-dialog.js";

export interface PasskeyEnrollmentClient {
  begin: (options: { signal?: AbortSignal }) => Promise<PasskeyEnrollment | null>;
  confirm: (input: PasskeyEnrollmentConfirmation) => Promise<string[] | null>;
  reverify: (confirmation: MFAVerification) => Promise<void>;
}

export const defaultPasskeyEnrollmentClient: PasskeyEnrollmentClient = {
  begin: beginPasskeyEnrollment,
  confirm: confirmPasskeyEnrollment,
  reverify: reverifyMFA,
};

export function PasskeyEnrollmentForm({
  client = defaultPasskeyEnrollmentClient,
  canReverifyPasskey = false,
  onEnrolled,
  onBusy,
}: {
  client?: PasskeyEnrollmentClient;
  canReverifyPasskey?: boolean;
  onEnrolled: (codes: string[] | null) => void;
  onBusy?: (busy: boolean) => void;
}): ReactNode {
  const [label, setLabel] = useState("");
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const [awaitingPasskey, setAwaitingPasskey] = useState(false);
  const [stepUp, setStepUp] = useState(false);
  const pending = useRef<AbortController | null>(null);
  const active = useRef(true);
  const createButton = useRef<HTMLButtonElement>(null);
  const restoreFocus = useRef(false);

  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
      pending.current?.abort();
    };
  }, []);
  useEffect(() => {
    if (!busy && restoreFocus.current) {
      restoreFocus.current = false;
      createButton.current?.focus();
    }
  }, [busy]);

  const create = async () => {
    if (pending.current || !active.current) return;
    const controller = new AbortController();
    pending.current = controller;
    setError(undefined);
    setBusy(true);
    onBusy?.(true);
    restoreFocus.current = true;
    setAwaitingPasskey(true);
    let recoveryCodes: string[] | null | undefined;
    try {
      const enrollment = await client.begin({ signal: controller.signal });
      setAwaitingPasskey(false);
      if (!enrollment || controller.signal.aborted) return;
      const name = label.trim();
      recoveryCodes = await client.confirm({ ...enrollment, ...(name ? { label: name } : {}) });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (isAppError(err) && (err.code === "mfa_reverify_required" || err.code === "mfa_required")) {
        setStepUp(true);
      } else if (err instanceof DOMException && err.name === "InvalidStateError") {
        setError("This authenticator already has a passkey for this account. Use another device or security key.");
      } else {
        setError(
          isAppError(err) && err.code === "mfa_enrollment_not_found"
            ? "That setup expired. Try creating your passkey again."
            : "Couldn't add your passkey. Try again or choose another method.",
        );
      }
    } finally {
      pending.current = null;
      setBusy(false);
      setAwaitingPasskey(false);
      onBusy?.(false);
    }
    if (recoveryCodes !== undefined && active.current) onEnrolled(recoveryCodes);
  };

  const reverify = async (confirmation: MFAVerification) => {
    await client.reverify(confirmation);
    if (!active.current) return;
    setStepUp(false);
    // A submitted registration ceremony is single-use, even when assurance has expired.
    void create();
  };

  const reverifyPasskey = async (signal: AbortSignal) => {
    const assertion = await requestPasskeyAssertion({ signal });
    if (assertion && !signal.aborted) await reverify(assertion);
  };

  if (stepUp) {
    return (
      <div className="flex flex-col gap-4">
        <p className="text-sm text-text-secondary">Confirm it's you before adding a passkey.</p>
        <MFACodeForm
          submitLabel="Continue"
          onSubmit={(confirmation: MFACodeConfirmation) => reverify(confirmation)}
          onPasskey={canReverifyPasskey ? reverifyPasskey : undefined}
        />
      </div>
    );
  }

  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    void create();
  };

  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-4">
      <p className="text-sm text-text-secondary">
        Use your device's screen lock or a security key to create a passkey for this account.
      </p>
      <FieldWrapper label="Name" description="Optional. Helps you tell your passkeys apart, e.g. “Laptop”.">
        <TextInput value={label} maxLength={64} disabled={busy} onChange={setLabel} />
      </FieldWrapper>
      {error && (
        <p role="alert" className="text-danger text-sm">
          {error}
        </p>
      )}
      <div role="status" aria-live="polite" className="text-sm text-text-secondary empty:hidden">
        {busy ? "Follow your browser's instructions…" : null}
      </div>
      {awaitingPasskey && (
        <Button variant="ghost" onClick={() => pending.current?.abort()}>
          Cancel passkey
        </Button>
      )}
      <Button ref={createButton} type="submit" variant="primary" fullWidth loading={busy}>
        Create passkey
      </Button>
    </form>
  );
}
