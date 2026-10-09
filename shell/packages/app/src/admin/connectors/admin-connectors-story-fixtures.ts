import {
  type FakeConnector,
  type FakeConnectorsBackendOptions,
  installFakeAdminConnectorsBackend,
} from "./fake-admin-connectors-backend.js";

// Shared by the connector stories and route tests. Paystack stores a
// plaintext secret and a generated webhook secret, so a test can prove
// neither reaches the page.
export const PAYSTACK: FakeConnector = {
  name: "connector_paystack",
  displayName: "Paystack",
  description: "Accept card and mobile money payments.",
  version: "1.2.0",
  webhooks: true,
  webhookToken: "tok43paystack",
  status: { configured: true, test_mode: false },
  config: [
    {
      key: "secret_key",
      label: "Secret Key",
      description: "Found in your Paystack dashboard.",
      type: "string",
      category: "API Credentials",
      required: true,
      encrypted: true,
      stored: "sk_live_plaintext_value",
    },
    {
      key: "webhook_secret",
      label: "Webhook Secret",
      type: "string",
      category: "API Credentials",
      encrypted: true,
      generated: true,
      stored: "whsec_generated",
    },
    { key: "test_mode", label: "Test Mode", type: "boolean", category: "Options", default: false },
    {
      key: "currency",
      label: "Currency",
      type: "string",
      category: "Options",
      default: "GHS",
      options: [
        { value: "GHS", label: "Ghana cedi" },
        { value: "NGN", label: "Nigerian naira" },
      ],
    },
    { key: "retries", label: "Retries", type: "integer", category: "Options", default: 3, min: 0, max: 5 },
    {
      key: "channels",
      label: "Channels",
      type: "string[]",
      category: "Options",
      default: ["card"],
      options: [
        { value: "card", label: "Card" },
        { value: "momo", label: "Mobile money" },
      ],
    },
    { key: "reference_prefix", label: "Reference prefix", type: "string", category: "Options", restartRequired: true },
  ],
};

export const TWILIO: FakeConnector = {
  name: "connector_twilio",
  displayName: "Twilio",
  version: "2.0.1",
  categories: ["sms_provider"],
  config: [{ key: "sender_id", label: "Sender ID", type: "string" }],
};

export const AFRICASTALKING: FakeConnector = {
  name: "connector_africastalking",
  displayName: "Africa's Talking",
  version: "1.0.0",
  categories: ["sms_provider"],
  status: { fail: "provider_unreachable" },
  config: [
    { key: "api_key", label: "API Key", type: "string", required: true, encrypted: true },
    { key: "username", label: "Username", type: "string", required: true },
  ],
};

export const STORY_CONNECTORS: FakeConnector[] = [PAYSTACK, TWILIO, AFRICASTALKING];

// A story's beforeEach: installs the in-memory backend and returns its cleanup.
export function fakeConnectorsBackend(options: Partial<FakeConnectorsBackendOptions> = {}) {
  return () => {
    const backend = installFakeAdminConnectorsBackend({
      connectors: STORY_CONNECTORS,
      primary: { sms_provider: "connector_twilio" },
      ...options,
    });
    return backend.restore;
  };
}
