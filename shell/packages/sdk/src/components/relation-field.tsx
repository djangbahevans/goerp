import type { ReactNode } from "react";

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
    <span>
      {label !== undefined && <span>{label}: </span>}
      {value === undefined ? (
        <span>{emptyText}</span>
      ) : href !== undefined ? (
        <a href={href}>{value.display}</a>
      ) : (
        <span>{value.display}</span>
      )}
    </span>
  );
}
