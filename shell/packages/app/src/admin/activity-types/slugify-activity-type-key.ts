// shell-ux.md §5.10 "Type sheet" — Key generation from the default-locale
// label, while the admin hasn't edited the key field themselves.
export function slugifyActivityTypeKey(label: string): string {
  const stripped = label
    .normalize("NFKD")
    .replace(/\p{M}/gu, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
  const withPrefix = stripped === "" ? "type" : /^[a-z]/.test(stripped) ? stripped : `type_${stripped}`;
  return withPrefix.slice(0, 40).replace(/_+$/, "") || "type";
}
