import { Button, Checkbox } from "@goerp/sdk/components";
import { type ReactNode, useState } from "react";
import { type PasskeyEnrollmentClient, PasskeyEnrollmentForm } from "../auth/passkey-enrollment-form.js";
import { RecoveryCodesList } from "../auth/totp-enrollment-parts.js";
import { SideSheet } from "../chrome/side-sheet.js";

export function AddPasskeySheet({
  open,
  client,
  canReverifyPasskey,
  onEnrolled,
  onClose,
}: {
  open: boolean;
  client: PasskeyEnrollmentClient;
  canReverifyPasskey: boolean;
  onEnrolled: () => void;
  onClose: (enrolled: boolean) => void;
}): ReactNode {
  const [busy, setBusy] = useState(false);
  const [codes, setCodes] = useState<string[] | null>(null);
  const [saved, setSaved] = useState(false);

  return (
    <SideSheet
      open={open}
      onClose={() => {
        if (!busy && (!codes || saved)) onClose(codes !== null);
      }}
      title="Add passkey"
    >
      <div className="flex flex-col gap-4 p-4">
        {codes ? (
          <>
            <p className="text-sm text-text-secondary">
              Passkey added. Save these recovery codes somewhere safe. Each one signs you in once if you lose access to
              your two-factor method, and they won't be shown again.
            </p>
            <RecoveryCodesList codes={codes} />
            <Checkbox label="I've saved these codes" checked={saved} onChange={setSaved} />
            <Button variant="primary" fullWidth disabled={!saved} onClick={() => onClose(true)}>
              Done
            </Button>
          </>
        ) : (
          <PasskeyEnrollmentForm
            client={client}
            canReverifyPasskey={canReverifyPasskey}
            onBusy={setBusy}
            onEnrolled={(recoveryCodes) => {
              onEnrolled();
              if (recoveryCodes?.length) setCodes(recoveryCodes);
              else onClose(true);
            }}
          />
        )}
      </div>
    </SideSheet>
  );
}
