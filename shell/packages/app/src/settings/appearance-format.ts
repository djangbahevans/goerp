import type { DateFormat } from "@goerp/sdk/auth";

// A locale's name in its own language ("English", "Français", "العربية"),
// or the code when the runtime can't name it.
export function nativeLocaleName(locale: string): string {
  try {
    const name = new Intl.DisplayNames([locale], { type: "language" }).of(locale);
    if (!name || name === locale) return locale;
    return name.charAt(0).toLocaleUpperCase(locale) + name.slice(1);
  } catch {
    return locale;
  }
}

// date in the given order, with the month named in locale: "16 May 2026",
// "May 16, 2026", "2026-05-16".
export function formatDateAs(date: Date, format: DateFormat, locale: string): string {
  const day = date.getDate();
  const year = date.getFullYear();
  if (format === "iso") {
    return `${year}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
  }
  const month = new Intl.DateTimeFormat(locale, { month: "short" }).format(date);
  return format === "day_first" ? `${day} ${month} ${year}` : `${month} ${day}, ${year}`;
}

// "Africa/Accra — 10:22", or null when the zone isn't one the runtime knows.
export function timezonePreview(timezone: string, now: Date, locale: string): string | null {
  try {
    const time = new Intl.DateTimeFormat(locale, { timeStyle: "short", timeZone: timezone }).format(now);
    return `${timezone} — ${time}`;
  } catch {
    return null;
  }
}
