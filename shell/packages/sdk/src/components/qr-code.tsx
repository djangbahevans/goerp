import { create } from "qrcode";
import type { ReactNode } from "react";
import { useId, useMemo } from "react";

const QUIET_ZONE = 4;
// Version 40 at medium error correction holds at most 5,596 numeric characters.
const MAX_CHARACTERS = 5596;

export interface QRCodeProps {
  value: string;
  label?: string | undefined;
}

function encode(value: string): { path: string; size: number } | null {
  if (value === "" || value.length > MAX_CHARACTERS) return null;

  try {
    // Reject lone surrogates before the encoder's TextEncoder can replace them.
    encodeURIComponent(value);
    const { modules } = create(value, { errorCorrectionLevel: "M" });
    const path: string[] = [];

    for (let row = 0; row < modules.size; row++) {
      for (let column = 0; column < modules.size; column++) {
        if (modules.get(row, column)) {
          path.push(`M${column + QUIET_ZONE} ${row + QUIET_ZONE}h1v1h-1z`);
        }
      }
    }

    return { path: path.join(""), size: modules.size + QUIET_ZONE * 2 };
  } catch {
    return null;
  }
}

export function QRCode({ value, label }: QRCodeProps): ReactNode {
  const captionId = useId();
  const code = useMemo(() => encode(value), [value]);

  if (value === "") {
    return (
      <span className="text-text-secondary">
        <span aria-hidden="true">—</span>
        <span className="sr-only">No value</span>
      </span>
    );
  }

  return (
    <figure className="flex min-w-0 max-w-full flex-col gap-2">
      {code ? (
        <svg
          role="img"
          aria-label={label ? `QR code for ${label}` : "QR code"}
          aria-describedby={captionId}
          viewBox={`0 0 ${code.size} ${code.size}`}
          width={Math.max(192, code.size * 4)}
          height={Math.max(192, code.size * 4)}
          className="h-auto max-w-full self-start"
          shapeRendering="crispEdges"
        >
          <rect width={code.size} height={code.size} fill="#FFFFFF" />
          <path d={code.path} fill="#000000" />
        </svg>
      ) : (
        <p className="text-danger text-sm">This value cannot be displayed as a QR code.</p>
      )}
      <figcaption id={captionId} className="select-text whitespace-pre-wrap text-sm text-text [overflow-wrap:anywhere]">
        {value}
      </figcaption>
    </figure>
  );
}
