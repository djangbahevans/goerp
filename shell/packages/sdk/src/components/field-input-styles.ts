// Shared bordered-input chrome (docs/components/text-input.md "States").
// "input" styles the focusable element itself; "wrapper" styles a box around
// it (TextInput with slots, the editors), so the interactive states key off
// the nested control via :focus-within/:has(). Read-only matches the
// attribute, not :read-only, which also matches every button and div.
const FONT_CLASSES = {
  mono: "font-mono",
  sans: "font-sans",
} as const;

const STATE_CLASSES = {
  input: [
    "focus:border-primary focus:shadow-focus placeholder:text-text-secondary",
    "[&[readonly]]:bg-bg-subtle [&[readonly]]:hover:border-border-control",
    "disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:border-border-control",
  ].join(" "),
  wrapper: [
    "focus-within:border-primary focus-within:shadow-focus",
    "[&_input]:placeholder:text-text-secondary [&_textarea]:placeholder:text-text-secondary",
    "has-[[readonly]]:bg-bg-subtle has-[[readonly]]:hover:border-border-control",
    "has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-50 has-[:disabled]:hover:border-border-control",
  ].join(" "),
} as const;

export function fieldInputClassName(
  hasError: boolean,
  appliesTo: "input" | "wrapper" = "input",
  // Mono is for numeral columns only (shell-visual-design.md §5).
  font: keyof typeof FONT_CLASSES = "sans",
): string {
  const border = hasError ? "border-danger" : "border-border-control hover:border-text-secondary";
  return `rounded-control border bg-surface px-3 py-2 ${FONT_CLASSES[font]} text-base text-text transition-colors duration-(--duration-fast) ease-out motion-reduce:transition-none ${STATE_CLASSES[appliesTo]} ${border}`;
}
