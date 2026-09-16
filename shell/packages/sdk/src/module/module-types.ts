import type { QueryClient } from "@tanstack/react-query";
import type { lazy } from "react";
import type { CurrentTenant, CurrentUser } from "../auth/types.js";
import type { AppError } from "../error/app-error.js";
import type { ToastAPI } from "../notifications/toast.js";
import type { NavigationGroup } from "../schema/view-registry.js";

export type LazyComponent = ReturnType<typeof lazy>;

// typescript-sdk-reference.md §3's ErrorHandlerContext, minus `navigate` —
// no useNavigate hook is threaded into useAction (the sole caller), so it's
// omitted rather than faked.
export interface ErrorHandlerContext {
  queryClient: QueryClient;
  toast: ToastAPI;
}

export type ErrorHandler = (err: AppError, ctx: ErrorHandlerContext) => void;

export interface CommandContext {
  navigate: (path: string) => void;
  user: CurrentUser;
  tenant: CurrentTenant;
  queryClient: QueryClient;
  toast: ToastAPI;
}

export interface CommandDefinition {
  id: string;
  label: string;
  shortcut?: string;
  permission?: string;
  icon?: string;
  keywords?: string[];
  action: (ctx: CommandContext) => void | Promise<void>;
}

export interface NavigationContext {
  user: CurrentUser;
  tenant: CurrentTenant;
}

// manifest-spec.md §8b Strategy 2's extension-field batch loading — per
// view, id-to-extension-field-values; a different contract from
// @goerp/sdk/schema's BatchLoaderRegistry (per relation column, id-to-label).
export type ExtensionBatchLoader = (recordIds: string[]) => Promise<Map<string, Record<string, unknown>>>;

export interface ModuleDefinition {
  name: string;
  // fieldRenderers keys self-namespace as `{module}:{renderer_name}`; views
  // keys are bare component names. Both pass through to ComponentRegistry as given.
  views?: Record<string, LazyComponent>;
  fieldRenderers?: Record<string, LazyComponent>;
  // Keyed `{target_module}.{target_view_name}`, one loader per view.
  batchLoaders?: Record<string, ExtensionBatchLoader>;
  commands?: CommandDefinition[];
  // Returning null uses the manifest's own navigation declarations as-is.
  navigation?: ((ctx: NavigationContext) => NavigationGroup[] | null) | null;
  // Keyed by exact AppError.code, with "*" as this module's wildcard fallback.
  errorHandlers?: Record<string, ErrorHandler>;
  // Accepted and returned as-is; not yet called by anything (hot-reload
  // orchestration, view-system.md's Module lifecycle section, is unbuilt).
  dispose?: () => void;
}
