import { useLocation } from "@tanstack/react-router";
import { useState } from "react";
import type { Command, CommandContext } from "./command-types.js";

const MAX_RECENT = 10;

// shell-architecture.md §18 source 2: the last 10 pages visited, most
// recent first — revisiting a page moves it back to the front rather than
// duplicating it. Local component state, not a module-level singleton:
// the command palette is a permanently-mounted app singleton, so there's
// only ever one instance of this hook to hold it.
//
// Recorded during render (React's "adjusting state when a prop changes"
// pattern), not in an effect — recordedFor tracks which pathname the
// currently-held `pathnames` already reflects, so a real navigation is
// still recorded exactly once despite this running on every render.
export function useRecentCommands(): Command[] {
  const location = useLocation();
  const [pathnames, setPathnames] = useState<string[]>([]);
  const [recordedFor, setRecordedFor] = useState<string | null>(null);

  if (recordedFor !== location.pathname) {
    setRecordedFor(location.pathname);
    setPathnames((prev) => [location.pathname, ...prev.filter((p) => p !== location.pathname)].slice(0, MAX_RECENT));
  }

  return pathnames.map((pathname) => ({
    id: `recent:${pathname}`,
    label: pathname,
    group: "Recent",
    action: (ctx: CommandContext) => ctx.navigate(pathname),
  }));
}
