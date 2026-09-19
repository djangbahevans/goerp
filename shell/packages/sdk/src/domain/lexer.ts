import { DomainExpressionError } from "./errors.js";

export type TokenKind =
  | "eof"
  | "ident"
  | "string"
  | "number"
  | "dot"
  | "lparen"
  | "rparen"
  | "comma"
  | "eq"
  | "neq"
  | "lt"
  | "gt"
  | "lte"
  | "gte"
  | "plus"
  | "minus"
  | "star"
  | "slash"
  | "and"
  | "or"
  | "not"
  | "in"
  | "is"
  | "null"
  | "true"
  | "false"
  | "like"
  | "ilike"
  | "child_of"
  | "parent_of";

export interface Token {
  kind: TokenKind;
  text: string;
  pos: number;
}

// Keywords are case-insensitive, identifiers are not, as in the Go lexer.
const KEYWORDS: ReadonlyMap<string, TokenKind> = new Map<string, TokenKind>([
  ["and", "and"],
  ["or", "or"],
  ["not", "not"],
  ["in", "in"],
  ["is", "is"],
  ["null", "null"],
  ["true", "true"],
  ["false", "false"],
  ["like", "like"],
  ["ilike", "ilike"],
  ["child_of", "child_of"],
  ["parent_of", "parent_of"],
]);

const NUMBER_LITERAL = /^\d+(\.\d+)?$/;

function isDigit(c: string): boolean {
  return c >= "0" && c <= "9";
}

function isIdentStart(c: string): boolean {
  return c === "_" || (c >= "a" && c <= "z") || (c >= "A" && c <= "Z");
}

function isIdentPart(c: string): boolean {
  return isIdentStart(c) || isDigit(c);
}

function isSpace(c: string): boolean {
  return c === " " || c === "\t" || c === "\n" || c === "\r" || c === "\v" || c === "\f";
}

const SINGLE_CHAR: Record<string, TokenKind> = {
  ".": "dot",
  "(": "lparen",
  ")": "rparen",
  ",": "comma",
  "=": "eq",
};

const ARITHMETIC_CHAR: Record<string, TokenKind> = {
  "+": "plus",
  "-": "minus",
  "*": "star",
  "/": "slash",
};

export interface LexOptions {
  // `+ - * /` exist only in the computed_display grammar (manifest-spec.md §8).
  arithmetic: boolean;
}

export function tokenize(src: string, options: LexOptions): Token[] {
  const tokens: Token[] = [];
  let pos = 0;

  const fail = (message: string, at: number): never => {
    throw new DomainExpressionError("parse", message, src, at);
  };

  while (true) {
    while (pos < src.length && isSpace(src.charAt(pos))) pos++;
    if (pos >= src.length) {
      tokens.push({ kind: "eof", text: "", pos });
      return tokens;
    }

    const start = pos;
    const c = src.charAt(pos);
    const next = src.charAt(pos + 1);

    const single = SINGLE_CHAR[c];
    if (single !== undefined) {
      pos++;
      tokens.push({ kind: single, text: c, pos: start });
      continue;
    }

    const arithmetic = options.arithmetic ? ARITHMETIC_CHAR[c] : undefined;
    if (arithmetic !== undefined) {
      pos++;
      tokens.push({ kind: arithmetic, text: c, pos: start });
      continue;
    }

    if (c === "!") {
      if (next !== "=") fail(`unexpected character "!"`, start);
      pos += 2;
      tokens.push({ kind: "neq", text: "!=", pos: start });
      continue;
    }

    if (c === "<" || c === ">") {
      const orEqual = next === "=";
      pos += orEqual ? 2 : 1;
      const kind: TokenKind = c === "<" ? (orEqual ? "lte" : "lt") : orEqual ? "gte" : "gt";
      tokens.push({ kind, text: orEqual ? `${c}=` : c, pos: start });
      continue;
    }

    if (c === "'") {
      pos++;
      let value = "";
      while (true) {
        if (pos >= src.length) fail("unterminated string literal", start);
        const ch = src.charAt(pos);
        if (ch === "'") {
          // SQL-style escaping: '' inside a string is a literal quote.
          if (src.charAt(pos + 1) === "'") {
            value += "'";
            pos += 2;
            continue;
          }
          pos++;
          break;
        }
        value += ch;
        pos++;
      }
      tokens.push({ kind: "string", text: value, pos: start });
      continue;
    }

    if (isDigit(c)) {
      while (pos < src.length && (isDigit(src.charAt(pos)) || src.charAt(pos) === ".")) pos++;
      const text = src.slice(start, pos);
      if (!NUMBER_LITERAL.test(text)) fail(`invalid number literal "${text}"`, start);
      tokens.push({ kind: "number", text, pos: start });
      continue;
    }

    if (isIdentStart(c)) {
      while (pos < src.length && isIdentPart(src.charAt(pos))) pos++;
      const text = src.slice(start, pos);
      tokens.push({ kind: KEYWORDS.get(text.toLowerCase()) ?? "ident", text, pos: start });
      continue;
    }

    fail(`unexpected character "${c}"`, start);
  }
}
