import { PermissionContext } from "@goerp/sdk/auth";
import { MODAL_OVERLAY_CLASSES, useListboxNavigation } from "@goerp/sdk/components";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { useLocation } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { formatShortcutText } from "../shortcuts/shortcut.js";
import { useBuiltInCommands } from "./built-in-commands.js";
import { onCommandPaletteOpenRequest } from "./command-palette-control.js";
import { commandRegistry } from "./command-registry.js";
import { searchCommands } from "./command-search.js";
import type { Command } from "./command-types.js";
import { useCommandRunner } from "./use-command-runner.js";
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

export function CommandPalette(): ReactNode {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const triggerRef = useRef<HTMLElement | null>(null);

  const runCommand = useCommandRunner();
  const permissions = useContext(PermissionContext);
  if (!permissions) {
    throw new Error("CommandPalette must be used within a PermissionProvider");
  }

  const builtInCommands = useBuiltInCommands();
  const { pathname } = useLocation();
  // The current page is excluded: navigating to it is a no-op.
  const recentCommands = useRecentCommands().filter((c) => c.id !== `recent:${pathname}`);
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

  const execute = (command: Command) => {
    if (runCommand(command)) setOpen(false);
  };

  // command-palette.md: the highlight clamps at either end rather than wrapping.
  const nav = useListboxNavigation({
    count: results.length,
    isOpen: open,
    wrap: false,
    homeEnd: query === "",
    onChoose: (index) => {
      const command = results[index];
      if (command) execute(command);
    },
  });

  useEffect(() => onCommandPaletteOpenRequest(() => setOpen(true)), []);

  useEffect(() => {
    if (open) {
      triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      // biome-ignore lint/nursery/useReactCompiler: each open starts from an empty query and the first result highlighted, the same reset AlertDialog's open effect does.
      setQuery("");
      nav.setActiveIndex(0);
    }
  }, [open, nav.setActiveIndex]);

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
              {...nav.inputProps}
              value={query}
              onChange={(event) => {
                setQuery(event.target.value);
                nav.setActiveIndex(0);
              }}
              onKeyDown={nav.handleKeyDown}
              placeholder="Type a command or search…"
              className="border-border border-b bg-transparent px-4 py-3 text-base text-text placeholder:text-text-secondary focus:outline-none"
            />
            <div id={nav.listboxId} role="listbox" aria-live="polite" className="flex-1 overflow-y-auto p-2">
              {results.length === 0 && query.trim() !== "" ? (
                <p className="px-2 py-3 text-sm text-text-secondary">No results for "{query}"</p>
              ) : results.length === 0 ? (
                <p className="px-2 py-3 text-sm text-text-secondary">
                  Start typing to search pages, records and commands.
                </p>
              ) : (
                rows.map(({ command, showGroupHeader }, index) => (
                  <div key={command.id}>
                    {showGroupHeader && (
                      <p className="px-2 pt-2 pb-1 text-text-secondary text-xs">{groupOf(command)}</p>
                    )}
                    <div
                      {...nav.getOptionProps(index)}
                      data-icon={command.icon}
                      className={`flex cursor-pointer items-center justify-between gap-3 rounded-control px-2 py-2 ${
                        index === nav.activeIndex ? "bg-surface-hover" : ""
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
                          {formatShortcutText(command.shortcut)}
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
