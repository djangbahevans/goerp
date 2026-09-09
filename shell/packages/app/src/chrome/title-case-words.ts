// Shared by route-breadcrumb.tsx's humanize() and user-menu.tsx's
// displayNameFromEmail() — same split/capitalize-first-letter/join
// pipeline, differing only in which characters delimit words.
export function titleCaseWords(text: string, delimiters: RegExp): string {
  return text
    .split(delimiters)
    .filter(Boolean)
    .map((word) => (word[0] ? word[0].toUpperCase() + word.slice(1) : word))
    .join(" ");
}
