import type { ReactNode } from "react";
import { TextLink } from "./text-link.js";

export interface RelationValue {
  id: string;
  display: string;
}

export interface RelationFieldProps {
  label?: string | undefined;
  value?: RelationValue | undefined;
  href?: string | undefined;
  emptyText?: string | undefined;
}

export function RelationField({ label, value, href, emptyText = "—" }: RelationFieldProps): ReactNode {
  return (
    <span className="flex flex-col gap-1">
      {label !== undefined && <span className="text-sm text-text-secondary">{label}</span>}
      {value === undefined ? (
        <span className="text-base text-text">{emptyText}</span>
      ) : href !== undefined ? (
        <span className="text-base">
          <TextLink href={href} inline>
            {value.display}
          </TextLink>
        </span>
      ) : (
        <span className="text-base text-text">{value.display}</span>
      )}
    </span>
  );
}
