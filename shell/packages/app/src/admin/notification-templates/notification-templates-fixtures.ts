import type { FakeTemplate, FakeTemplateType } from "./fake-notification-templates-backend.js";

// Shared by the notification templates tests and stories: an engine type,
// a sales type with defaults, an override and a language added with no
// default, and a type that ships no templates.

export const FIXTURE_TYPES: FakeTemplateType[] = [
  {
    type: "engine.activity_assigned",
    module: "engine",
    label: "Activity Assigned",
    availableChannels: ["in_app", "email", "push"],
    dataSchema: { ActivitySummary: "string", TypeIcon: "string" },
  },
  {
    type: "sales.order_confirmed",
    module: "sales",
    label: "Order Confirmed",
    availableChannels: ["in_app", "email", "sms", "push"],
    dataSchema: { OrderReference: "string", AmountTotal: "float", Paid: "bool" },
  },
  {
    type: "sales.quote_expiring",
    module: "sales",
    label: "Quote Expiring",
    availableChannels: ["in_app", "email"],
  },
];

export function fixtureTemplates(): FakeTemplate[] {
  return [
    {
      type: "engine.activity_assigned",
      channel: "in_app",
      locale: "en",
      default: { title_template: "{{.ActivitySummary}}", icon: "{{.TypeIcon}}" },
    },
    {
      type: "sales.order_confirmed",
      channel: "in_app",
      locale: "en",
      default: {
        title_template: "Order {{.OrderReference}} confirmed",
        body_template: "Total {{.AmountTotal}}",
        icon: "shopping-cart",
      },
    },
    {
      type: "sales.order_confirmed",
      channel: "email",
      locale: "en",
      default: {
        subject_template: "Order {{.OrderReference}} confirmed",
        html_template: "<p>Hello {{.UserFirstName}},</p>\n<p>Your order {{.OrderReference}} is confirmed.</p>",
        text_template: "Hello {{.UserFirstName}}, your order {{.OrderReference}} is confirmed.",
      },
      override: {
        subject_template: "Thanks! Order {{.OrderReference}} is confirmed",
        html_template: "<h1>Thank you</h1>\n<p>Order {{.OrderReference}} is on its way.</p>",
      },
    },
    {
      type: "sales.order_confirmed",
      channel: "sms",
      locale: "en",
      default: { sms_template: "{{.TenantName}}: Order {{.OrderReference}} confirmed." },
    },
    {
      type: "sales.order_confirmed",
      channel: "sms",
      locale: "fr",
      override: { sms_template: "{{.TenantName}} : commande {{.OrderReference}} confirmée." },
    },
  ];
}
