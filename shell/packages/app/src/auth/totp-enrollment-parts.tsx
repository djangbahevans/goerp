import type { TOTPEnrollment } from "@goerp/sdk/auth";
import { Button } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import type { ReactNode } from "react";

// Pieces of shell-ux.md §2.7's TOTP setup shared by the forced-enrollment
// wizard and the Security page's "Add authenticator app" sheet.

const RECOVERY_FILE_NAME = "goerp-recovery-codes.txt";

// Groups the base32 key in fours so it can be read off and typed by hand.
export function formatManualKey(secret: string): string {
  return secret.match(/.{1,4}/g)?.join(" ") ?? secret;
}

function svgDataURL(svg: string): string {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
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

// The QR code and its manual-entry key.
export function TOTPScanDetails({ enrollment }: { enrollment: TOTPEnrollment }): ReactNode {
  return (
    <>
      <img
        src={svgDataURL(enrollment.qrSvg)}
        alt="QR code to add this account to your authenticator app"
        width={176}
        height={176}
        className="mx-auto rounded-control border border-border bg-white p-2"
      />
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
    </>
  );
}

// A new set of recovery codes, with copy and download.
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
