// Shared bordered-input chrome across MoneyField/DateField/DateTimeField/
// TimeField (docs/components/*-field.md "Tokens Used"). DateField/
// DateTimeField/TimeField apply this directly to their <input> ("input"),
// so the interactive states key off the element's own :focus/:disabled.
// MoneyField applies it to a wrapping span instead ("wrapper"), to share
// one bordered box with its currency-code prefix — there, the same states
// have to key off the nested <input> via :focus-within/:has(:disabled).
export function fieldInputClassName(hasError: boolean, appliesTo: "input" | "wrapper" = "input"): string {
  const interactive =
    appliesTo === "input"
      ? "focus:border-primary focus:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
      : "focus-within:border-primary focus-within:shadow-focus has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-50";
  return `rounded-control border px-3 py-2 font-mono text-sm transition-colors duration-(--duration-fast) ease-out ${interactive} ${
    hasError ? "border-danger" : "border-border"
  }`;
}
