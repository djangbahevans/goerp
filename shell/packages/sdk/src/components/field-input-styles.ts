// Shared bordered-input chrome across MoneyField/DateField/DateTimeField/
// TimeField/TagsField (docs/components/*-field.md "Tokens Used"). DateField/
// DateTimeField/TimeField/TagsField apply this directly to their <input>
// ("input"), so the interactive states key off the element's own
// :focus/:disabled. MoneyField applies it to a wrapping span instead
// ("wrapper"), to share one bordered box with its currency-code prefix —
// there, the same states have to key off the nested <input> via
// :focus-within/:has(:disabled).
const FONT_CLASSES = {
  mono: "font-mono",
  sans: "font-sans",
} as const;

export function fieldInputClassName(
  hasError: boolean,
  appliesTo: "input" | "wrapper" = "input",
  // RichTextField is the one caller that needs body-text sans, not
  // code-style mono (docs/components/rich-text-field.md "Tokens Used"
  // contrasts it explicitly against CodeField's mono).
  font: keyof typeof FONT_CLASSES = "mono",
): string {
  const interactive =
    appliesTo === "input"
      ? "focus:border-primary focus:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
      : "focus-within:border-primary focus-within:shadow-focus has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-50";
  return `rounded-control border px-3 py-2 ${FONT_CLASSES[font]} text-sm transition-colors duration-(--duration-fast) ease-out ${interactive} ${
    hasError ? "border-danger" : "border-border"
  }`;
}
