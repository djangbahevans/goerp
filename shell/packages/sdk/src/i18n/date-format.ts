import type { DateFormat } from "../auth/types.js";

export interface DatePreferences {
  timezone: string;
  currency: string;
  dateOrder: DateFormat | null;
}

export const DEFAULT_DATE_PREFERENCES: DatePreferences = { timezone: "UTC", currency: "", dateOrder: null };

export class DatePreferencesStore {
  private value: DatePreferences = DEFAULT_DATE_PREFERENCES;
  private readonly listeners = new Set<() => void>();

  get = (): DatePreferences => this.value;

  set(value: DatePreferences): void {
    if (
      value.timezone === this.value.timezone &&
      value.currency === this.value.currency &&
      value.dateOrder === this.value.dateOrder
    )
      return;
    this.value = { ...value };
    for (const listener of this.listeners) listener();
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
}

export const datePreferencesStore = new DatePreferencesStore();
export type DatePreferencesStoreLike = Pick<DatePreferencesStore, "get" | "subscribe">;
export type DatePreset = "short" | "medium";

// Intl has no date-order option; localized parts preserve digits and month names when reordered.
class OrderedDateFormatter extends Intl.DateTimeFormat {
  constructor(
    locale: string,
    private readonly order: DateFormat,
    private readonly preset: DatePreset,
    timezone: string,
  ) {
    super(locale, {
      timeZone: timezone,
      calendar: "gregory",
      year: "numeric",
      month: order === "iso" || preset === "short" ? "2-digit" : "short",
      day: order === "iso" || preset === "short" ? "2-digit" : "numeric",
    });
  }

  override formatToParts(value?: Date | number): Intl.DateTimeFormatPart[] {
    const parts = super.formatToParts(value);
    const part = (type: "year" | "month" | "day"): Intl.DateTimeFormatPart => {
      const result = parts.find((part) => part.type === type);
      if (!result) throw new RangeError(`Missing date part: ${type}`);
      return result;
    };
    const literal = (value: string): Intl.DateTimeFormatPart => ({ type: "literal", value });
    if (this.order === "iso") return [part("year"), literal("-"), part("month"), literal("-"), part("day")];
    if (this.preset === "short") {
      const first = this.order === "day_first" ? "day" : "month";
      const second = this.order === "day_first" ? "month" : "day";
      return [part(first), literal("/"), part(second), literal("/"), part("year")];
    }
    return this.order === "day_first"
      ? [part("day"), literal(" "), part("month"), literal(" "), part("year")]
      : [part("month"), literal(" "), part("day"), literal(", "), part("year")];
  }

  override readonly format = (value?: Date | number): string =>
    this.formatToParts(value)
      .map((part) => part.value)
      .join("");

  override formatRangeToParts(start: Date | number, end: Date | number): Intl.DateTimeRangeFormatPart[] {
    if (!Number.isFinite(Number(start)) || !Number.isFinite(Number(end)) || Number(start) > Number(end)) {
      throw new RangeError("Invalid date range");
    }
    if (Number(start) === Number(end)) return this.formatToParts(start).map((part) => ({ ...part, source: "shared" }));
    return [
      ...this.formatToParts(start).map((part): Intl.DateTimeRangeFormatPart => ({ ...part, source: "startRange" })),
      { type: "literal", value: " – ", source: "shared" },
      ...this.formatToParts(end).map((part): Intl.DateTimeRangeFormatPart => ({ ...part, source: "endRange" })),
    ];
  }

  override formatRange(start: Date | number, end: Date | number): string {
    return this.formatRangeToParts(start, end)
      .map((part) => part.value)
      .join("");
  }
}

export function createDateFormatter(
  locale: string,
  preferences: DatePreferences,
  preset: DatePreset = "medium",
): Intl.DateTimeFormat {
  return preferences.dateOrder === null
    ? new Intl.DateTimeFormat(locale, { dateStyle: preset, timeZone: preferences.timezone })
    : new OrderedDateFormatter(locale, preferences.dateOrder, preset, preferences.timezone);
}

const PATTERN_FIELDS: Record<string, Intl.DateTimeFormatOptions> = {
  y: { year: "numeric" },
  yy: { year: "2-digit" },
  yyyy: { year: "numeric" },
  M: { month: "numeric" },
  MM: { month: "2-digit" },
  MMM: { month: "short" },
  MMMM: { month: "long" },
  MMMMM: { month: "narrow" },
  d: { day: "numeric" },
  dd: { day: "2-digit" },
  E: { weekday: "short" },
  EE: { weekday: "short" },
  EEE: { weekday: "short" },
  EEEE: { weekday: "long" },
  EEEEE: { weekday: "narrow" },
};

export function formatDatePattern(value: Date | number, pattern: string, locale: string, timezone: string): string {
  if (!Number.isFinite(Number(value))) throw new RangeError("Invalid date");
  let result = "";
  let quoted = false;
  for (let index = 0; index < pattern.length; ) {
    const char = pattern.charAt(index);
    if (char === "'") {
      if (pattern[index + 1] === "'") {
        result += "'";
        index += 2;
        continue;
      }
      quoted = !quoted;
      index += 1;
      continue;
    }
    if (quoted || !/[A-Za-z]/.test(char)) {
      result += char;
      index += 1;
      continue;
    }
    let end = index + 1;
    while (pattern[end] === char) end += 1;
    const token = pattern.slice(index, end);
    const field = PATTERN_FIELDS[token];
    if (!field) throw new RangeError(`Unsupported date pattern token: ${token}`);
    const type = Object.keys(field)[0];
    const parts = new Intl.DateTimeFormat(locale, {
      year: "numeric",
      month: "numeric",
      day: "numeric",
      ...field,
      calendar: "gregory",
      timeZone: timezone,
    }).formatToParts(value);
    if (["y", "yy", "yyyy", "M", "MM", "d", "dd"].includes(token)) {
      const numericParts = new Intl.DateTimeFormat("en", {
        year: "numeric",
        month: "numeric",
        day: "numeric",
        calendar: "gregory",
        numberingSystem: "latn",
        timeZone: timezone,
      }).formatToParts(value);
      let number = Number(numericParts.find((part) => part.type === type)?.value);
      if (token === "yy") number %= 100;
      result += new Intl.NumberFormat(locale, { useGrouping: false, minimumIntegerDigits: token.length }).format(
        number,
      );
    } else {
      result += parts.find((part) => part.type === type)?.value ?? "";
    }
    index = end;
  }
  if (quoted) throw new RangeError("Unclosed date pattern literal");
  return result;
}
