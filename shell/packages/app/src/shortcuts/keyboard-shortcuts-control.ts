// Same prop-less open request as command-palette-control.ts, so the
// shortcuts, the user menu and the palette command can all open the dialog.
const listeners = new Set<() => void>();

export function openKeyboardShortcuts(): void {
  for (const listener of listeners) listener();
}

export function onKeyboardShortcutsOpenRequest(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
