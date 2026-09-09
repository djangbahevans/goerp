import type { Command } from "./command-types.js";

// shell-architecture.md §18: "fzf-style scoring" — every character of the
// query must appear in the target in order (a subsequence match); score
// rewards an early match start and contiguous runs, since those read as
// closer to what the user actually typed.
export function fuzzyScore(query: string, target: string): number | null {
  if (query === "") return 0;
  const q = query.toLowerCase();
  const t = target.toLowerCase();

  let score = 0;
  let targetIndex = 0;
  let runLength = 0;
  let firstMatchIndex = -1;

  for (let queryIndex = 0; queryIndex < q.length; queryIndex++) {
    const char = q[queryIndex] as string;
    const foundAt = t.indexOf(char, targetIndex);
    if (foundAt === -1) return null;

    if (firstMatchIndex === -1) firstMatchIndex = foundAt;
    runLength = foundAt === targetIndex ? runLength + 1 : 1;
    score += 1 + runLength; // a contiguous run scores more than isolated hits
    targetIndex = foundAt + 1;
  }

  // Rewards a match starting near the beginning of the target (e.g. a
  // prefix match) over the same subsequence found deep inside it.
  score -= firstMatchIndex * 0.1;
  return score;
}

function matchedFields(query: string, command: Command): number[] {
  const scores = [fuzzyScore(query, command.label)];
  if (command.description) scores.push(fuzzyScore(query, command.description));
  for (const keyword of command.keywords ?? []) scores.push(fuzzyScore(query, keyword));
  return scores.filter((score): score is number => score !== null);
}

// Filters to commands with at least one matching field, ranked by their
// best-matching field's score (ties keep the given order — a stable sort).
export function searchCommands(commands: Command[], query: string): Command[] {
  if (query.trim() === "") return commands;
  return commands
    .map((command) => ({ command, score: Math.max(-Infinity, ...matchedFields(query, command)) }))
    .filter(({ score }) => score !== -Infinity)
    .sort((a, b) => b.score - a.score)
    .map(({ command }) => command);
}
