// Lets a future trigger outside CommandPalette itself (chrome-header.md's
// SearchTrigger, goerp#709) open the palette without it taking any props —
// command-palette.md's API/Props section is explicit that it takes none.
const listeners = new Set<() => void>();

export function openCommandPalette(): void {
  for (const listener of listeners) listener();
}

export function onCommandPaletteOpenRequest(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
