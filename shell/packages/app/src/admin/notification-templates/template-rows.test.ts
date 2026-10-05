import { describe, expect, it } from "vitest";
import type { TemplateType } from "./notification-templates-api.js";
import { fallbackLocale, templateGroups } from "./template-rows.js";

function type(overrides: Partial<TemplateType> & Pick<TemplateType, "type" | "module" | "label">): TemplateType {
  return { availableChannels: ["in_app", "email", "sms"], templates: [], ...overrides };
}

const entry = (channel: "in_app" | "email" | "sms", locale: string, customised = false, hasDefault = true) => ({
  channel,
  locale,
  customised,
  hasDefault,
});

const TYPES: TemplateType[] = [
  type({
    type: "sales.order_confirmed",
    module: "sales",
    label: "Order Confirmed",
    templates: [
      entry("sms", "fr", true, false),
      entry("email", "en", true),
      entry("in_app", "fr"),
      entry("in_app", "en"),
    ],
  }),
  type({ type: "crm.lead_won", module: "crm", label: "Lead Won", templates: [entry("in_app", "en")] }),
  type({ type: "engine.activity_due", module: "engine", label: "Activity Due", templates: [entry("in_app", "en")] }),
  type({ type: "sales.quote_expiring", module: "sales", label: "Quote Expiring" }),
];

const OPTIONS = {
  displayNameOf: (module: string) => ({ sales: "Sales", crm: "CRM" })[module] ?? module,
  defaultLocale: "fr",
  search: "",
  customisedOnly: false,
};

describe("templateGroups", () => {
  it("puts General first, then modules by display name, rows by label, channel and language", () => {
    const groups = templateGroups(TYPES, OPTIONS);
    expect(groups.map((g) => g.name)).toEqual(["General", "CRM", "Sales"]);
    expect(groups[2]?.rows.map((r) => r.key)).toEqual([
      "sales.order_confirmed/in_app/fr",
      "sales.order_confirmed/in_app/en",
      "sales.order_confirmed/email/en",
      "sales.order_confirmed/sms/fr",
      "sales.quote_expiring",
    ]);
  });

  it("matches a search against the label or the type string", () => {
    expect(templateGroups(TYPES, { ...OPTIONS, search: "lead" }).map((g) => g.name)).toEqual(["CRM"]);
    expect(templateGroups(TYPES, { ...OPTIONS, search: "ENGINE.activity" }).map((g) => g.name)).toEqual(["General"]);
  });

  it("keeps only customised rows, and no empty-type rows, when filtered to customised", () => {
    const groups = templateGroups(TYPES, { ...OPTIONS, customisedOnly: true });
    expect(groups.flatMap((g) => g.rows.map((r) => r.key))).toEqual([
      "sales.order_confirmed/email/en",
      "sales.order_confirmed/sms/fr",
    ]);
  });
});

describe("fallbackLocale", () => {
  it("falls back to the base language, then en", () => {
    expect(fallbackLocale(TYPES, "sales.order_confirmed", "in_app", "fr-GH")).toBe("fr");
    expect(fallbackLocale(TYPES, "sales.order_confirmed", "in_app", "pt-BR")).toBe("en");
  });

  it("is null when no template exists to fall back to", () => {
    expect(fallbackLocale(TYPES, "sales.order_confirmed", "sms", "de")).toBeNull();
    expect(fallbackLocale(TYPES, "sales.order_confirmed", "in_app", "en")).toBeNull();
  });
});
