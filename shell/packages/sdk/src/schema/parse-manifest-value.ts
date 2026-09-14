import * as v from "valibot";
import { summarizeIssues } from "./summarize-issues.js";

export type ParseManifestResult<T> = { success: true; output: T } | { success: false; message: string };

// Shared by every caller validating a resolved manifest value against a
// valibot schema, reducing a failed parse to one reusable message.
export function parseManifestValue<T>(schema: v.GenericSchema<unknown, T>, value: unknown): ParseManifestResult<T> {
  const result = v.safeParse(schema, value);
  if (result.success) return { success: true, output: result.output };
  return { success: false, message: summarizeIssues(result.issues) };
}
