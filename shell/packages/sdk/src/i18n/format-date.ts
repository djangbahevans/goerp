import { createDateFormatter, datePreferencesStore, formatDatePattern } from "./date-format.js";
import { localeStore } from "./use-locale.js";

export function formatDate(value: Date | number, presetOrPattern: string = "medium"): string {
  const locale = localeStore.getLocale();
  const preferences = datePreferencesStore.get();
  if (presetOrPattern === "short" || presetOrPattern === "medium") {
    return createDateFormatter(locale, preferences, presetOrPattern).format(value);
  }
  return formatDatePattern(value, presetOrPattern, locale, preferences.timezone);
}

export function formatDateTime(value: Date | number): string {
  const locale = localeStore.getLocale();
  const preferences = datePreferencesStore.get();
  if (preferences.dateOrder === null) {
    return new Intl.DateTimeFormat(locale, {
      dateStyle: "medium",
      timeStyle: "short",
      timeZone: preferences.timezone,
    }).format(value);
  }
  const time = new Intl.DateTimeFormat(locale, { timeStyle: "short", timeZone: preferences.timezone }).format(value);
  return `${createDateFormatter(locale, preferences).format(value)}, ${time}`;
}

const RELATIVE_TIME_DIVISIONS: [number, Intl.RelativeTimeFormatUnit][] = [
  [60, "second"],
  [60, "minute"],
  [24, "hour"],
  [7, "day"],
  [4.34524, "week"],
  [12, "month"],
  [Number.POSITIVE_INFINITY, "year"],
];

export function formatRelativeTime(value: Date | number): string {
  let duration = (Number(value) - Date.now()) / 1000;
  const formatter = new Intl.RelativeTimeFormat(localeStore.getLocale(), { numeric: "always" });
  for (const [amount, unit] of RELATIVE_TIME_DIVISIONS) {
    const rounded = Math.sign(duration) * Math.round(Math.abs(duration));
    if (Math.abs(rounded) < amount) return formatter.format(rounded, unit);
    duration /= amount;
  }
  return formatter.format(Math.round(duration), "year");
}
