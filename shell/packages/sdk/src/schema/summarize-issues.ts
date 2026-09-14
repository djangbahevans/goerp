import * as v from "valibot";

// Shared by every validation boundary in this module (SchemaRegistry,
// resolveViewDeclaration) that needs a short, human-readable summary of
// why a value failed to parse.
export function summarizeIssues(issues: readonly v.BaseIssue<unknown>[]): string {
  return issues
    .slice(0, 5)
    .map((issue) => `${v.getDotPath(issue) ?? "(root)"}: ${issue.message}`)
    .join("; ");
}
