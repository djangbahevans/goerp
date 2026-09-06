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

export function countryNameOf(code: string): string {
  try {
    return new Intl.DisplayNames(undefined, { type: "region" }).of(code.toUpperCase()) ?? code;
  } catch {
    return code;
  }
}

export function CountryFlag({ code, showName = false }: CountryFlagProps): ReactNode {
  const name = countryNameOf(code);
  return (
    <span>
      <span role="img" aria-label={name}>
        {flagOf(code)}
      </span>
      {showName && <span> {name}</span>}
    </span>
  );
}
