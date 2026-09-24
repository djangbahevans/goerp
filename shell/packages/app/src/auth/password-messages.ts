export const PASSWORD_MISMATCH = "Passwords don't match.";

// The server's policy messages are lowercase fragments ("password is too
// common"); shown inline they read as sentences.
export function policyMessageAsSentence(message: string): string {
  const trimmed = message.trim();
  if (!trimmed) return trimmed;
  const capitalized = trimmed[0]?.toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(capitalized) ? capitalized : `${capitalized}.`;
}
