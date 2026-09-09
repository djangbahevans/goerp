import { PermissionContext } from "@goerp/sdk/auth";
import { MODAL_OVERLAY_CLASSES } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import type { KeyboardEvent, ReactNode } from "react";
import { useContext, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useBuiltInCommands } from "./built-in-commands.js";
import { onCommandPaletteOpenRequest } from "./command-palette-control.js";
import { commandRegistry } from "./command-registry.js";
import { searchCommands } from "./command-search.js";
import type { Command, CommandContext } from "./command-types.js";
import { useRecentCommands } from "./use-recent-commands.js";

// Same full-viewport-boundary/inner-panel split as AlertDialog's CONTENT_CLASSES.
const CONTENT_CLASSES =
  "fixed inset-0 z-(--z-modal) flex items-center justify-center p-4 focus:outline-none data-[state=open]:animate-[command-palette-content-show_var(--duration-slow)_ease-out] data-[state=closed]:animate-[command-palette-content-hide_var(--duration-slow)_ease-in] motion-reduce:data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] motion-reduce:data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in]";

function groupOf(command: Command): string {
  return command.group ?? "Commands";
}

interface Row {
  command: Command;
  showGroupHeader: boolean;
}

// Options stay a flat, single ranked sequence (command-palette.md: "Sources
// merge into one ranked, re-grouped list... a top hit from Recent can
// outrank a lower-scoring built-in command") — group headers are inserted
// wherever the group changes, not a separate list per group.
function toRows(commands: Command[]): Row[] {
  let previousGroup: string | undefined;
  return commands.map((command) => {
    const group = groupOf(command);
    const showGroupHeader = group !== previousGroup;
    previousGroup = group;
    return { command, showGroupHeader };
  });
}

interface InputState {
  query: string;
  highlightedIndex: number;
}

const INITIAL_INPUT_STATE: InputState = { query: "", highlightedIndex: 0 };

export function CommandPalette(): ReactNode {
  const [open, setOpen] = useState(false);
  // query and highlightedIndex change together everywhere they change (a
  // fresh query always resets the highlight) — one state atom instead of
  // two separate setState calls per update site.
  const [{ query, highlightedIndex }, setInputState] = useState<InputState>(INITIAL_INPUT_STATE);
  const triggerRef = useRef<HTMLElement | null>(null);
  const highlightedRef = useRef<HTMLDivElement | null>(null);
  const listboxId = useId();

  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const permissions = useContext(PermissionContext);
  if (!permissions) {
    throw new Error("CommandPalette must be used within a PermissionProvider");
  }

  const builtInCommands = useBuiltInCommands();
  const recentCommands = useRecentCommands();
  const registeredCommands = useSyncExternalStore(
    (listener) => commandRegistry.subscribe(listener),
    () => commandRegistry.getAll(),
  );

  const visibleCommands = useMemo(
    () => [...builtInCommands, ...registeredCommands].filter((c) => !c.permission || permissions.check(c.permission)),
    [builtInCommands, registeredCommands, permissions],
  );

  const results = query.trim() === "" ? recentCommands : searchCommands([...visibleCommands, ...recentCommands], query);
  const rows = toRows(results);
  const activeIndex = Math.max(0, Math.min(highlightedIndex, results.length - 1));

  useEffect(() => onCommandPaletteOpenRequest(() => setOpen(true)), []);

  useEffect(() => {
    function handleKeyDown(event: globalThis.KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen(true);
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, []);

  useEffect(() => {
    if (open) {
      triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      // biome-ignore lint/nursery/useReactCompiler: resetting to the initial input state on reopen — same reset-on-reopen shape as AlertDialog's own (already-shipped) triggerRef/setState effect.
      setInputState(INITIAL_INPUT_STATE);
    }
  }, [open]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: activeIndex is a deliberate change-trigger, not read inside the effect — it drives which row highlightedRef currently points at.
  useEffect(() => {
    highlightedRef.current?.scrollIntoView({ block: "nearest" });
    // biome-ignore lint/nursery/useReactCompiler: scrollIntoView is a DOM side effect keyed on activeIndex, not a state update.
  }, [activeIndex]);

  const context: CommandContext = { navigate: (path) => void navigate({ to: path }), toast, queryClient };

  const execute = (command: Command) => {
    setOpen(false);
    command.action(context);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        setInputState((s) => ({ ...s, highlightedIndex: Math.min(s.highlightedIndex + 1, results.length - 1) }));
        break;
      case "ArrowUp":
        event.preventDefault();
        setInputState((s) => ({ ...s, highlightedIndex: Math.max(s.highlightedIndex - 1, 0) }));
        break;
      case "Enter": {
        event.preventDefault();
        const command = results[activeIndex];
        if (command) execute(command);
        break;
      }
      default:
        break;
    }
  };

  return (
    <DialogPrimitive.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) setOpen(false);
      }}
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
        <DialogPrimitive.Content
          aria-label="Command palette"
          className={CONTENT_CLASSES}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            triggerRef.current?.focus();
          }}
        >
          <div className="flex max-h-[70vh] w-full max-w-[min(560px,calc(100vw-32px))] flex-col rounded-structural border border-border bg-surface shadow-lg">
            <input
              type="text"
              role="combobox"
              aria-expanded={open}
              aria-controls={listboxId}
              aria-autocomplete="list"
              aria-activedescendant={results.length > 0 ? `${listboxId}-option-${activeIndex}` : undefined}
              value={query}
              onChange={(event) => setInputState({ query: event.target.value, highlightedIndex: 0 })}
              onKeyDown={handleKeyDown}
              placeholder="Type a command or search…"
              className="border-border border-b bg-transparent px-4 py-3 text-base text-text placeholder:text-text-secondary focus:outline-none"
            />
            <div id={listboxId} role="listbox" aria-live="polite" className="flex-1 overflow-y-auto p-2">
              {results.length === 0 && query.trim() !== "" ? (
                <p className="px-2 py-3 text-sm text-text-secondary">No results for "{query}"</p>
              ) : results.length === 0 ? null : (
                rows.map(({ command, showGroupHeader }, index) => (
                  <div key={command.id}>
                    {showGroupHeader && (
                      <p className="px-2 pt-2 pb-1 text-text-secondary text-xs">{groupOf(command)}</p>
                    )}
                    {/* biome-ignore lint/a11y/useFocusableInteractive: ARIA combobox-with-listbox — options are never independently focusable, only virtually tracked via aria-activedescendant. */}
                    {/* biome-ignore lint/a11y/useKeyWithClickEvents: keyboard selection goes through the input's own onKeyDown (ArrowUp/ArrowDown/Enter). */}
                    <div
                      id={`${listboxId}-option-${index}`}
                      role="option"
                      aria-selected={index === activeIndex}
                      ref={index === activeIndex ? highlightedRef : undefined}
                      onMouseEnter={() => setInputState((s) => ({ ...s, highlightedIndex: index }))}
                      onClick={() => execute(command)}
                      data-icon={command.icon}
                      className={`flex cursor-pointer items-center justify-between gap-3 rounded-control px-2 py-2 ${
                        index === activeIndex ? "bg-surface-hover" : ""
                      }`}
                    >
                      <span className="flex min-w-0 flex-col">
                        <span className="text-sm text-text">{command.label}</span>
                        {command.description && (
                          <span className="truncate text-text-secondary text-xs">{command.description}</span>
                        )}
                      </span>
                      {command.shortcut && (
                        <kbd className="rounded-control border border-border px-1.5 py-0.5 text-text-secondary text-xs">
                          {command.shortcut}
                        </kbd>
                      )}
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
