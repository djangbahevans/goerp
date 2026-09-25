import { Fragment, type ReactNode } from "react";
import type { ShortcutGroup } from "./shell-shortcuts.js";
import { describeShortcut, formatStep, IS_MAC, type Shortcut } from "./shortcut.js";
import { type ShortcutEntry, useShortcuts } from "./use-shortcuts.js";

const GROUP_ORDER: readonly ShortcutGroup[] = ["General", "Navigation", "Commands"];

const KBD_CLASSES = "rounded-control border border-border px-1.5 py-0.5 text-text-secondary text-xs";

function Keys({ shortcuts }: { shortcuts: Shortcut[] }): ReactNode {
  return shortcuts.map((shortcut, alternative) => (
    // biome-ignore lint/suspicious/noArrayIndexKey: an entry's alternatives are a fixed list.
    <Fragment key={alternative}>
      {alternative > 0 && <span className="text-text-secondary text-xs">or</span>}
      {shortcut.map((step, index) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: a shortcut's steps are a fixed sequence.
        <Fragment key={index}>
          {index > 0 && <span className="text-text-secondary text-xs">then</span>}
          <kbd className={KBD_CLASSES}>{formatStep(step, IS_MAC)}</kbd>
        </Fragment>
      ))}
    </Fragment>
  ));
}

function spokenKeys(shortcuts: Shortcut[]): string {
  return shortcuts.map((shortcut) => describeShortcut(shortcut, IS_MAC)).join(" or ");
}

export interface KeyboardShortcutsListProps {
  headingLevel?: 3 | 4 | undefined;
}

// keyboard-shortcuts-dialog.md: the reference content, shared by
// KeyboardShortcutsDialog and the help panel.
export function KeyboardShortcutsList({ headingLevel = 3 }: KeyboardShortcutsListProps): ReactNode {
  const GroupHeading = headingLevel === 4 ? "h4" : "h3";
  const entries = useShortcuts();
  const groups = GROUP_ORDER.map((group) => ({
    group,
    entries: entries.filter((entry: ShortcutEntry) => entry.group === group),
  })).filter(({ entries: groupEntries }) => groupEntries.length > 0);

  return (
    <div className="flex flex-col gap-4">
      {groups.map(({ group, entries: groupEntries }) => (
        <section key={group}>
          <GroupHeading className="text-text-secondary text-xs">{group}</GroupHeading>
          <dl>
            {groupEntries.map((entry) => (
              <div key={entry.id} className="flex items-center justify-between gap-4 py-2">
                <dt className="text-sm text-text">{entry.label}</dt>
                <dd className="flex shrink-0 items-center gap-1">
                  <span className="sr-only">{spokenKeys(entry.shortcuts)}</span>
                  <span aria-hidden="true" className="flex items-center gap-1">
                    <Keys shortcuts={entry.shortcuts} />
                  </span>
                </dd>
              </div>
            ))}
          </dl>
        </section>
      ))}
    </div>
  );
}
