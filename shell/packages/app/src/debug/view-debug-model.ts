import type { FieldAccess } from "@goerp/sdk/auth";

export interface DeclarationElement {
  path: string;
  label: string;
}

const IDENTIFYING_KEYS = ["field", "name", "label", "action", "type"] as const;

function identify(value: Record<string, unknown>): string | null {
  for (const key of IDENTIFYING_KEYS) {
    const candidate = value[key];
    if (typeof candidate === "string" && candidate !== "") return `${key} "${candidate}"`;
  }
  return null;
}

// Every identifiable entry in the declaration's arrays (columns, sections, fields,
// tabs, actions, ...), with its path inside the view declaration.
export function declarationElements(declaration: Readonly<Record<string, unknown>>): DeclarationElement[] {
  const elements: DeclarationElement[] = [];
  const visit = (value: unknown, path: string, inArray: boolean) => {
    if (Array.isArray(value)) {
      value.forEach((item, index) => {
        visit(item, `${path}[${index}]`, true);
      });
      return;
    }
    if (typeof value !== "object" || value === null) return;
    const record = value as Record<string, unknown>;
    const label = inArray ? identify(record) : null;
    if (label) elements.push({ path, label });
    for (const [key, child] of Object.entries(record)) visit(child, path ? `${path}.${key}` : key, false);
  };
  visit(declaration, "", false);
  return elements;
}

export type FieldDecision = "shown" | "hidden" | "masked";

export interface FieldSecurityDecision {
  decision: FieldDecision;
  detail: string;
}

const DENIED_READ_DETAIL = {
  omit: "responses omit the field",
  nullify: "responses return null",
  mask: "responses return a masked value",
} as const;

// `access` is the field's /_meta/permissions entry; a field without one is unrestricted.
export function fieldSecurityDecision(access: FieldAccess | undefined, pending: boolean): FieldSecurityDecision {
  if (pending) return { decision: "hidden", detail: "permissions have not loaded" };
  if (!access) return { decision: "shown", detail: "no field access rule" };
  if (access.read) return { decision: "shown", detail: access.write ? "read and write" : "read only" };
  const deniedRead = access.denied_read ?? "omit";
  return {
    decision: deniedRead === "mask" ? "masked" : "hidden",
    detail: `no read access, so the shell doesn't render it; ${DENIED_READ_DETAIL[deniedRead]}`,
  };
}
