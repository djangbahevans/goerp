import type { CommandContext, CommandDefinition } from "@goerp/sdk/module";

export type { CommandContext };

// shell-architecture.md §18's Command — the data model the command palette
// merges three sources into and executes against. Extends @goerp/sdk's
// CommandDefinition (typescript-sdk-reference.md §3, the shape a module's
// defineModule().commands entries declare) with description/group, both
// presentational fields specific to how this shell's own palette renders
// and groups results — not part of the cross-module registration contract.
export interface Command extends CommandDefinition {
  description?: string;
  group?: string;
}
