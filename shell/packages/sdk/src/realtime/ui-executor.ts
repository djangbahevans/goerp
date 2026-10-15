import type { QueryClient } from "@tanstack/react-query";
import type { APIResponse } from "../http/types.js";
import { moduleLink } from "../nav/module-link.js";
import { toast } from "../notifications/toast.js";
import { currentExecutionContext, type ExecutionContext } from "./execution-context.js";
import { interpolatePath } from "./interpolate.js";
import { type DialogAction, type DialogField, modalManager } from "./modal-manager.js";

export type ToastSeverity = "success" | "error" | "warning" | "info";

export type UIInstruction =
  | { type: "toast"; message: string; severity: ToastSeverity; duration_ms?: number }
  | { type: "reload"; resource?: string; record_id?: string }
  | { type: "set_badge"; target: string; count: number }
  | { type: "navigate"; path: string }
  | { type: "open_form"; resource: string; record_id?: string }
  | {
      type: "open_dialog";
      title: string;
      submit_label?: string;
      action: DialogAction;
      fields: DialogField[];
      values?: Record<string, unknown>;
    }
  | { type: "close_modal" }
  | { type: "open_url"; url: string; target?: "blank" | "self" };

// "push" instructions arrive over the WebSocket with no user gesture behind them.
export type InstructionSource = "response" | "push";

export const UI_ENVELOPE_HEADER = "X-GoERP-UI";

const PUSHABLE = new Set<string>(["toast", "reload", "set_badge"]);
const DEFAULT_TOAST_DURATION_MS = 4000;
const TOAST_SEVERITIES = new Set<string>(["success", "error", "warning", "info"]);

export interface UIExecutorHost {
  queryClient: QueryClient;
  navigate: (href: string) => void | Promise<void>;
}

let host: UIExecutorHost | null = null;

// Called once by the shell at startup. Until then, `reload`, `set_badge` and
// `navigate` are skipped with a warning.
export function configureUIExecutor(next: UIExecutorHost | null): void {
  host = next;
}

function requireHost(type: string): UIExecutorHost | null {
  if (!host) console.warn(`ui: ${type} needs a configured UI executor; skipped`);
  return host;
}

function reloadMatches(instruction: Extract<UIInstruction, { type: "reload" }>, ctx: ExecutionContext): boolean {
  const { resource, record_id } = instruction;
  if (!resource) return true;
  if (record_id) return ctx.view === "form" && ctx.resource === resource && ctx.recordId === record_id;
  return ctx.view === "list" && ctx.resource === resource;
}

function isWebURL(url: string): boolean {
  try {
    const { protocol } = new URL(url);
    return protocol === "https:" || protocol === "http:";
  } catch {
    return false;
  }
}

// Resolves false when the rest of the sequence must not run.
async function executeOne(instruction: UIInstruction, ctx: ExecutionContext): Promise<boolean> {
  switch (instruction.type) {
    case "toast":
      toast[TOAST_SEVERITIES.has(instruction.severity) ? instruction.severity : "info"](instruction.message, {
        duration: instruction.duration_ms ?? DEFAULT_TOAST_DURATION_MS,
      });
      break;
    case "reload": {
      if (!ctx.queryKey || !reloadMatches(instruction, ctx)) break;
      await requireHost(instruction.type)?.queryClient.invalidateQueries({ queryKey: ctx.queryKey });
      break;
    }
    case "set_badge":
      requireHost(instruction.type)?.queryClient.setQueryData(["nav-badge", instruction.target], {
        count: instruction.count,
      });
      break;
    case "navigate":
      await requireHost(instruction.type)?.navigate(moduleLink(interpolatePath(instruction.path, ctx.record)));
      break;
    case "close_modal":
      modalManager.closeTop();
      break;
    case "open_url":
      // A javascript: or data: URL from a response would run in the shell's origin.
      if (!isWebURL(instruction.url)) {
        console.warn(`ui: open_url needs an http(s) URL; skipped ${instruction.url}`);
        break;
      }
      if (instruction.target === "self") window.location.assign(instruction.url);
      else window.open(instruction.url, "_blank", "noopener,noreferrer");
      break;
    case "open_dialog": {
      const result = await modalManager.openDialog({
        title: instruction.title,
        submitLabel: instruction.submit_label ?? "Submit",
        action: instruction.action,
        fields: instruction.fields,
        record: ctx.record,
        values: instruction.values ?? {},
      });
      return !result.cancelled;
    }
    case "open_form":
      modalManager.open({
        resource: instruction.resource,
        ...(instruction.record_id !== undefined ? { recordId: instruction.record_id } : {}),
      });
      break;
    default:
      console.warn(`ui: unknown instruction type ${(instruction as { type: string }).type}; skipped`);
  }
  return true;
}

// Runs instructions in order, each finishing before the next starts. Each reads
// the execution context when it starts, so a reload after a navigate targets
// the view that navigate opened. A cancelled open_dialog ends the sequence.
export async function executeInstructions(
  instructions: readonly UIInstruction[],
  source: InstructionSource,
): Promise<void> {
  for (const instruction of instructions) {
    if (source === "push" && !PUSHABLE.has(instruction.type)) {
      console.warn(`ui: ${instruction.type} can't be pushed; skipped`);
      continue;
    }
    try {
      if (!(await executeOne(instruction, currentExecutionContext()))) return;
    } catch (err) {
      // The action already succeeded on the server; failing the call would
      // invite a retry of a request that must not run twice.
      console.error(`ui: ${instruction.type} failed`, err);
    }
  }
}

// Returns an action response's result. A UI envelope runs its instructions and
// yields its `data`; any other body is returned unchanged, after the current
// view re-fetches unless the action was a read.
export async function resolveActionResponse<T>(method: string, response: APIResponse<unknown>): Promise<T> {
  if (response.headers.get(UI_ENVELOPE_HEADER) === "1") {
    const envelope = response.body as { data?: unknown; ui?: UIInstruction[] } | undefined;
    await executeInstructions(envelope?.ui ?? [], "response");
    return envelope?.data as T;
  }
  const { queryKey } = currentExecutionContext();
  if (method !== "GET" && queryKey && host) await host.queryClient.invalidateQueries({ queryKey });
  return response.body as T;
}
