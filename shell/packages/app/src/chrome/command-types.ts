import type { ToastAPI } from "@goerp/sdk/notifications";
import type { QueryClient } from "@tanstack/react-query";

// shell-architecture.md §18's Command/CommandContext — the data model the
// command palette merges three sources into and executes against.
export interface Command {
  id: string;
  label: string;
  description?: string;
  keywords?: string[];
  icon?: string;
  shortcut?: string;
  permission?: string;
  group?: string;
  action: (ctx: CommandContext) => void;
}

export interface CommandContext {
  navigate: (path: string) => void;
  toast: ToastAPI;
  queryClient: QueryClient;
}
