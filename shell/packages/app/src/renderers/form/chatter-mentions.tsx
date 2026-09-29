import type { ActivityMention } from "@goerp/sdk/react";
import type { ReactNode } from "react";

// docs/components/form-chatter.md "Mentions in the composer" and "Mentions
// in comment bodies".

export const MAX_MENTION_QUERY = 50;

// A mention query in progress: the "@" at `start`, and the text after it
// up to the caret.
export interface MentionQuery {
  start: number;
  query: string;
}

function startsMention(text: string, at: number): boolean {
  return text[at] === "@" && (at === 0 || /\s/.test(text[at - 1] ?? ""));
}

// Starts a query when the character just typed, ending at `caret`, is an
// "@" at the start of the text or after whitespace.
export function mentionQueryStartedAt(text: string, caret: number): MentionQuery | null {
  const at = caret - 1;
  return at >= 0 && startsMention(text, at) ? { start: at, query: "" } : null;
}

// The query `start` began, as of `caret`, or null once it has ended: the
// caret left it, its "@" is gone, or it gained a line break, a second
// consecutive space, a leading space, or more than 50 characters.
export function continueMentionQuery(text: string, start: number, caret: number): MentionQuery | null {
  if (caret <= start || !startsMention(text, start)) return null;
  const query = text.slice(start + 1, caret);
  if (query.length > MAX_MENTION_QUERY || query.includes("\n") || query.includes("  ") || query.startsWith(" ")) {
    return null;
  }
  return { start, query };
}

// The text a chosen candidate is inserted as, after its "@": their name,
// or their email's local part when they have none.
export function mentionName(candidate: { name: string | null; email: string }): string {
  return candidate.name ?? candidate.email.split("@")[0] ?? candidate.email;
}

const WORD_CHAR = /[\p{L}\p{N}]/u;

// Replaces each recorded "@{name}" still in `text` with "<@{id}>". An
// occurrence counts only when its "@" starts the text or follows
// whitespace and the name isn't followed by a letter or digit; longer
// names are tried first, so "@Ama Owusu" is never read as "@Ama".
export function encodeMentions(text: string, recorded: ReadonlyMap<string, string>): string {
  const names = [...recorded.keys()].sort((a, b) => b.length - a.length);
  let out = "";
  let i = 0;
  while (i < text.length) {
    if (startsMention(text, i)) {
      const name = names.find((n) => text.startsWith(n, i + 1) && !WORD_CHAR.test(text[i + 1 + n.length] ?? ""));
      if (name !== undefined) {
        out += `<@${recorded.get(name)}>`;
        i += 1 + name.length;
        continue;
      }
    }
    out += text[i];
    i += 1;
  }
  return out;
}

const MENTION_TOKEN = /<@([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})>/g;

// How a mention of `id` reads: its name, else its email's local part, else
// "Unknown user" for someone who no longer exists.
export function mentionLabel(id: string, mentions: readonly ActivityMention[]): string {
  const mention = mentions.find((m) => m.id === id);
  if (mention?.name) return mention.name;
  if (mention?.email) return mention.email.split("@")[0] ?? mention.email;
  return "Unknown user";
}

// A comment body with each mention token rendered as "@{name}", a
// mention of the viewer highlighted.
export function renderCommentBody(
  body: string,
  mentions: readonly ActivityMention[],
  viewerId: string | undefined,
): ReactNode[] {
  const parts: ReactNode[] = [];
  let last = 0;
  for (const match of body.matchAll(MENTION_TOKEN)) {
    const id = match[1] ?? "";
    if (match.index > last) parts.push(body.slice(last, match.index));
    const own = id === viewerId;
    parts.push(
      <span
        key={match.index}
        className={`font-medium text-primary ${own ? "rounded-control bg-primary-subtle px-0.5" : ""}`}
      >
        @{mentionLabel(id, mentions)}
      </span>,
    );
    last = match.index + match[0].length;
  }
  if (last < body.length) parts.push(body.slice(last));
  return parts;
}
