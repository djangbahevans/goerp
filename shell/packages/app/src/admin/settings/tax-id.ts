export const TAX_ID_MAX_LENGTH = 100;

export function normalizeTaxID(value: string): string {
  // Go TrimSpace uses Unicode White_Space; JavaScript trim also removes BOMs and excludes NEL.
  return value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "");
}
