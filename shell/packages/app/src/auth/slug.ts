// Mirrors the engine's registration slug derivation (auth-internals.md §3
// "Slug derivation", internal/engine/auth/authregister/slug.go), so the
// availability check asks about the slug POST /auth/register will use.

const SLUG_PATTERN = /^[a-z][a-z0-9-]{1,62}[a-z0-9]$/;
const MAX_SLUG_LENGTH = 64;

export function deriveSlug(companyName: string): string {
  let slug = "";
  let pendingHyphen = false;
  for (const ch of companyName.normalize("NFKD")) {
    if (/\p{Mn}/u.test(ch)) continue;
    if (/^[A-Za-z0-9]$/.test(ch)) {
      if (pendingHyphen && slug.length > 0) slug += "-";
      pendingHyphen = false;
      slug += ch.toLowerCase();
    } else {
      pendingHyphen = true;
    }
  }
  if (/^[0-9]/.test(slug)) slug = `co-${slug}`;
  if (slug.length > MAX_SLUG_LENGTH) slug = slug.slice(0, MAX_SLUG_LENGTH).replace(/-+$/, "");
  return slug;
}

export function isValidSlug(slug: string): boolean {
  return SLUG_PATTERN.test(slug);
}
