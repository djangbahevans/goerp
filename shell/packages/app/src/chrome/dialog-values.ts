import { type DialogConfig, type DialogField, interpolate } from "@goerp/sdk/realtime";

// boolean for `boolean`, minor units for `monetary`, and the input's text for
// every other type, `number` included, so a half-typed number survives.
export type DialogValue = string | number | boolean | undefined;

function toMinorUnits(raw: unknown): number | undefined {
  if (typeof raw === "number") return Number.isFinite(raw) ? Math.round(raw) : undefined;
  if (typeof raw !== "string" || raw.trim() === "") return undefined;
  const n = Number(raw);
  return Number.isFinite(n) ? Math.round(n) : undefined;
}

function initialValue(field: DialogField, config: DialogConfig): DialogValue {
  const raw = Object.hasOwn(config.values, field.name)
    ? config.values[field.name]
    : interpolate(field.default ?? "", config.record);
  switch (field.type) {
    case "boolean":
      return raw === true || raw === "true";
    case "monetary":
      return toMinorUnits(raw);
    default:
      return raw == null ? "" : String(raw);
  }
}

export function initialDialogValues(config: DialogConfig): Record<string, DialogValue> {
  return Object.fromEntries(config.fields.map((field) => [field.name, initialValue(field, config)]));
}

export function isDialogFieldFilled(field: DialogField, value: DialogValue): boolean {
  switch (field.type) {
    case "boolean":
      return value === true;
    case "monetary":
      return value !== undefined;
    case "number":
      return typeof value === "string" && value.trim() !== "" && Number.isFinite(Number(value));
    default:
      return typeof value === "string" && value.trim() !== "";
  }
}

// An empty field is sent as null rather than "", which a numeric or date
// field on the server could not decode.
export function dialogRequestBody(
  fields: readonly DialogField[],
  values: Record<string, DialogValue>,
): Record<string, unknown> {
  return Object.fromEntries(
    fields.map((field) => {
      const value = values[field.name];
      if (field.type === "boolean") return [field.name, value === true];
      if (!isDialogFieldFilled(field, value)) return [field.name, null];
      return [field.name, field.type === "number" ? Number(value) : value];
    }),
  );
}
