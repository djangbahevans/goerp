import type { ReactNode } from "react";

export interface CountryFlagProps {
  // ISO 3166-1 alpha-2.
  code: string;
  showName?: boolean | undefined;
}

// Regional indicator symbols — matches column-renderers.tsx's countryFlag
// exactly, so a custom view's flag looks identical to the manifest-driven
// "country" list column type.
export function flagOf(code: string): string {
  return [...code.toUpperCase()].map((c) => String.fromCodePoint(0x1f1e6 + c.charCodeAt(0) - 65)).join("");
}

const REGION_NAMES = new Intl.DisplayNames(undefined, { type: "region" });

export function countryNameOf(code: string): string {
  try {
    return REGION_NAMES.of(code.toUpperCase()) ?? code;
  } catch {
    return code;
  }
}

const VALID_CODE = /^[A-Za-z]{2}$/;

export function CountryFlag({ code, showName = false }: CountryFlagProps): ReactNode {
  const name = countryNameOf(code);
  // An invalid code renders no flag glyph at all — just the raw code as
  // plain text — rather than feeding it through flagOf() and risking a
  // broken/nonsensical glyph sequence.
  const valid = VALID_CODE.test(code);
  return (
    <span className="inline-flex items-center gap-1">
      <span role="img" aria-label={name}>
        {valid ? flagOf(code) : code}
      </span>
      {showName && <span className="text-sm">{name}</span>}
    </span>
  );
}
