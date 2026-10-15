import { parsePhoneNumberFromString } from "libphonenumber-js/min";

export interface FormattedPhone {
  text: string;
  href: string;
}

// Parsing silently drops text it does not recognize, such as "(office)", so
// only values made of a number and an optional extension are formatted.
const FORMATTABLE = /^\+[\d\s().\-/]+(?:(?:ext\.?|extension|x|#)\s*\d+)?$/i;

/**
 * Returns a stored phone value's display text and `tel:` link.
 *
 * A value with a leading `+`, a recognized calling code and a possible length
 * displays in international format and links to its E.164 form, including any
 * extension. Any other value, including a national number, is returned
 * verbatim, because formatting it would require assuming a country.
 */
export function formatPhone(value: string): FormattedPhone {
  const trimmed = value.trim();
  const phone = FORMATTABLE.test(trimmed) ? parsePhoneNumberFromString(trimmed) : undefined;
  if (phone === undefined || !phone.isPossible()) return { text: value, href: `tel:${value}` };
  return { text: phone.formatInternational(), href: phone.format("RFC3966") };
}
