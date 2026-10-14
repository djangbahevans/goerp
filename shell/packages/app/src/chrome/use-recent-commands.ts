import { useLocation } from "@tanstack/react-router";
import { useState } from "react";
import type { Command, CommandContext } from "./command-types.js";

const MAX_RECENT = 10;

// Recording during render keeps each navigation in the history exactly once.
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
    action: (ctx: CommandContext) => ctx.navigate(pathname, { shell: true }),
  }));
}
