import { useEffect, useMemo, useState } from "react";
import { createSequenceMatcher, type ShortcutBinding } from "./sequence-matcher.js";
import { IS_MAC } from "./shortcut.js";
import { useShortcuts } from "./use-shortcuts.js";

const MODIFIER_KEYS = new Set(["Shift", "Control", "Alt", "Meta", "CapsLock", "AltGraph"]);

const NON_TEXT_INPUT_TYPES = new Set(["checkbox", "radio", "button", "submit", "reset", "range", "color", "file"]);

// Any open overlay: dialogs, sheets and popovers (role="dialog"/
// "alertdialog"), menus, and dropdown listboxes.
const OVERLAY_SELECTOR = '[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"]';

// Also widgets that take typed letters without being a text field, such as
// a select trigger's type-ahead.
const TYPING_WIDGET_SELECTOR =
  '[contenteditable]:not([contenteditable="false"]), [role="combobox"], [role="textbox"], [role="searchbox"], [role="spinbutton"]';

function isTextEntry(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target instanceof HTMLInputElement) return !NON_TEXT_INPUT_TYPES.has(target.type);
  if (target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return true;
  return target.closest(TYPING_WIDGET_SELECTOR) !== null;
}

// shell-ux.md §7.3's "When shortcuts are ignored".
function isIgnored(event: KeyboardEvent): boolean {
  if (event.defaultPrevented || event.isComposing || event.repeat) return true;
  if (document.querySelector(OVERLAY_SELECTOR)) return true;
  // Alt (Option on a Mac, AltGr as Ctrl+Alt elsewhere) types characters, so
  // only a Ctrl or Cmd chord without it counts as a command in a text field.
  const command = (event.ctrlKey || event.metaKey) && !event.altKey;
  return !command && isTextEntry(event.target);
}

// The one document-level keydown listener behind every global shortcut.
export function GlobalShortcuts(): null {
  const entries = useShortcuts();
  const [matcher] = useState(() => createSequenceMatcher(IS_MAC));
  const bindings = useMemo(
    () =>
      entries.flatMap(({ shortcuts, run }): ShortcutBinding[] =>
        run ? shortcuts.map((shortcut) => ({ shortcut, run })) : [],
      ),
    [entries],
  );

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent): void {
      if (MODIFIER_KEYS.has(event.key)) return;
      if (isIgnored(event)) {
        matcher.reset();
        return;
      }
      const binding = matcher.handle(event, bindings, Date.now());
      if (!binding) return;
      event.preventDefault();
      binding.run();
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [matcher, bindings]);

  return null;
}
