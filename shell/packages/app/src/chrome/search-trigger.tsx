import { Search } from "lucide-react";
import type { ReactNode } from "react";
import { openCommandPalette } from "./command-palette-control.js";

function isMac(): boolean {
  if (typeof navigator === "undefined") return false;
  // || not ?? — navigator.platform is a string, not nullable, so an empty
  // string (privacy-hardened browsers) needs the same fallthrough a
  // missing value would get.
  const platform = navigator.platform || navigator.userAgent || "";
  return /Mac|iPod|iPhone|iPad/.test(platform);
}

// Computed once per module load, not per render — the platform doesn't
// change during a session.
const shortcutHint = isMac() ? "⌘K" : "Ctrl K";
const shortcutLabel = isMac() ? "Cmd+K" : "Ctrl+K";

// chrome-header.md: opens the same CommandPalette the global shortcut does.
export function SearchTrigger(): ReactNode {
  return (
    <button
      type="button"
      onClick={openCommandPalette}
      aria-label={`Search (${shortcutLabel})`}
      className="flex items-center gap-2 rounded-control px-2 py-1.5 text-text-secondary hover:bg-surface-hover hover:text-text focus-visible:outline-none focus-visible:shadow-focus"
    >
      <Search size={18} aria-hidden="true" />
      {/* No aria-hidden: the button's own aria-label already covers this text, and biome's noAriaHiddenOnFocusable rejects it here anyway. */}
      <kbd className="rounded-control border border-border px-1.5 py-0.5 text-text-secondary text-xs">
        {shortcutHint}
      </kbd>
    </button>
  );
}
