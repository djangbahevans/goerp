import { Button, PasswordField } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { type ReactNode, type SubmitEvent, useEffect, useState } from "react";
import { MFADialog } from "../../settings/mfa-code-dialog.js";

export interface ResetMfaDialogProps {
  open: boolean;
  name: string;
  tenantName: string;
  onConfirm: (password: string) => Promise<void>;
  onClose: () => void;
}

// shell-ux.md §5.1: the admin confirms with their own password.
export function ResetMfaDialog({ open, name, tenantName, onConfirm, onClose }: ResetMfaDialogProps): ReactNode {
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | undefined>(undefined);
  const [busy, setBusy] = useState(false);

  // Reset on open, not on close, so the form stays filled through the exit animation.
  useEffect(() => {
    if (!open) return;
    setPassword("");
    setError(undefined);
  }, [open]);

  const submit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy || password === "") return;
    setBusy(true);
    setError(undefined);
    try {
      await onConfirm(password);
    } catch (err) {
      setPassword("");
      setError(
        isAppError(err) && err.code === "invalid_password"
          ? "Your password is incorrect."
          : "Couldn't reset two-factor authentication. Try again.",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <MFADialog
      open={open}
      title={`Reset two-factor authentication for ${name}?`}
      description={`${name} will be able to sign in to ${tenantName} with their password alone and set up a new method. Other organisations they belong to aren't affected.`}
      tone="warning"
      dismissible={!busy}
      onDismiss={onClose}
    >
      <form noValidate onSubmit={(event) => void submit(event)} className="flex flex-col gap-4">
        <PasswordField
          label="Your password"
          autoComplete="current-password"
          value={password}
          onChange={(value) => {
            setPassword(value);
            setError(undefined);
          }}
          error={error}
          disabled={busy}
        />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="danger" loading={busy} disabled={password === ""}>
            Reset two-factor
          </Button>
        </div>
      </form>
    </MFADialog>
  );
}
