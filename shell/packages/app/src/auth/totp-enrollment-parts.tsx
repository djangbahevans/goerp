import type { TOTPEnrollment } from "@goerp/sdk/auth";
import { Button, QRCode } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import type { ReactNode } from "react";

const RECOVERY_FILE_NAME = "goerp-recovery-codes.txt";

export function formatManualKey(secret: string): string {
  return secret.match(/.{1,4}/g)?.join(" ") ?? secret;
}

async function copyText(text: string, what: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
    toast.success(`${what} copied.`);
  } catch {
    toast.error(`Couldn't copy the ${what.toLowerCase()}. Select it and copy it manually.`);
  }
}

function downloadCodes(codes: string[]): void {
  const blob = new Blob([`${codes.join("\n")}\n`], { type: "text/plain" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = RECOVERY_FILE_NAME;
  a.click();
  URL.revokeObjectURL(url);
}

export function TOTPScanDetails({ enrollment }: { enrollment: TOTPEnrollment }): ReactNode {
  return (
    <QRCode
      svg={enrollment.qrSvg}
      label="authenticator app setup"
      align="center"
      caption={
        <div className="flex flex-col gap-1">
          <span className="text-sm text-text">Can't scan it? Enter this key instead</span>
          <div className="flex items-center gap-2">
            <code className="flex-1 select-all break-all rounded-control bg-bg-subtle px-2 py-1 font-mono text-sm text-text">
              {formatManualKey(enrollment.secret)}
            </code>
            <Button size="sm" aria-label="Copy setup key" onClick={() => void copyText(enrollment.secret, "Key")}>
              Copy
            </Button>
          </div>
        </div>
      }
    />
  );
}

export function RecoveryCodesList({ codes }: { codes: string[] }): ReactNode {
  return (
    <>
      <ol aria-label="Recovery codes" className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-control bg-bg-subtle p-3">
        {codes.map((recoveryCode) => (
          <li key={recoveryCode} className="font-mono text-sm text-text">
            {recoveryCode}
          </li>
        ))}
      </ol>
      <div className="flex gap-2">
        <Button size="sm" fullWidth onClick={() => void copyText(codes.join("\n"), "Recovery codes")}>
          Copy all
        </Button>
        <Button size="sm" fullWidth onClick={() => downloadCodes(codes)}>
          Download
        </Button>
      </div>
    </>
  );
}
