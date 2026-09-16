import type { CommandContext, CommandDefinition } from "@goerp/sdk/module";

export type { CommandContext };

// shell-architecture.md §18's Command. Extends @goerp/sdk's CommandDefinition
// with description/group — this shell's own presentational/grouping fields.
export interface Command extends CommandDefinition {
  description?: string;
  group?: string;
}
