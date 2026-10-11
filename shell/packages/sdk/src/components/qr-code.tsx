import { create } from "qrcode";
import type { ReactNode } from "react";
import { useId, useMemo, useState } from "react";

const QUIET_ZONE = 4;
// Version 40 at medium error correction holds at most 5,596 numeric characters.
const MAX_CHARACTERS = 5596;

export type QRCodeProps = {
  label?: string | undefined;
  align?: "start" | "center" | undefined;
} & ({ value: string; svg?: never; caption?: never } | { svg: string; caption: ReactNode; value?: never });

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

function svgDataURL(svg: string): string | null {
  if (svg.trim() === "") return null;

  try {
    return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  } catch {
    return null;
  }
}

export function QRCode({ value, svg, caption, label, align = "start" }: QRCodeProps): ReactNode {
  const captionId = useId();
  const [failedSVG, setFailedSVG] = useState<string | null>(null);
  const code = useMemo(() => (value === undefined ? null : encode(value)), [value]);
  const imageURL = useMemo(() => (svg === undefined ? null : svgDataURL(svg)), [svg]);
  const imageLabel = label ? `QR code for ${label}` : "QR code";
  const imageClass = `h-auto max-w-full ${align === "center" ? "self-center" : "self-start"}`;

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
      {svg !== undefined ? (
        imageURL && failedSVG !== svg ? (
          <img
            src={imageURL}
            alt={imageLabel}
            aria-describedby={captionId}
            width={192}
            height={192}
            className={`${imageClass} aspect-square bg-white object-contain p-2`}
            onError={() => setFailedSVG(svg)}
          />
        ) : (
          <p className="text-danger text-sm">This QR code cannot be displayed.</p>
        )
      ) : code ? (
        <svg
          role="img"
          aria-label={imageLabel}
          aria-describedby={captionId}
          viewBox={`0 0 ${code.size} ${code.size}`}
          width={Math.max(192, code.size * 4)}
          height={Math.max(192, code.size * 4)}
          className={imageClass}
          shapeRendering="crispEdges"
        >
          <rect width={code.size} height={code.size} fill="#FFFFFF" />
          <path d={code.path} fill="#000000" />
        </svg>
      ) : (
        <p className="text-danger text-sm">This value cannot be displayed as a QR code.</p>
      )}
      <figcaption id={captionId} className="wrap-anywhere select-text whitespace-pre-wrap text-sm text-text">
        {svg === undefined ? value : caption}
      </figcaption>
    </figure>
  );
}
