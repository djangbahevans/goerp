import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { withAdminShell } from "../users/admin-users-story-fixtures.js";
import {
  defaultDeliveryWire,
  defaultSettingsWire,
  type FakeTenantSettingsOptions,
  installFakeTenantSettingsBackend,
} from "./fake-tenant-settings-backend.js";
import { TenantSettingsPage } from "./tenant-settings-page.js";

function fakeBackend(options: FakeTenantSettingsOptions = {}) {
  return () => installFakeTenantSettingsBackend(options).restore;
}

const meta: Meta<typeof TenantSettingsPage> = {
  title: "Admin/Settings",
  component: TenantSettingsPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
};

export default meta;

type Story = StoryObj<typeof TenantSettingsPage>;

export const Default: Story = {
  name: "saved settings",
  beforeEach: fakeBackend({ secrets: { apiKey: "re_saved" } }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("heading", { name: "Email" })).toBeInTheDocument());
    expect(canvas.getByText("A key is saved. Enter a new one to replace it.")).toBeInTheDocument();
  },
};

const smtpDelivery = defaultDeliveryWire();
Object.assign(smtpDelivery.email as Record<string, unknown>, {
  provider: "smtp",
  from_addr: "noreply@acme.test",
  smtp: { host: "smtp.acme.test", port: 587, user: "mailer", password: "", use_tls: true },
});
smtpDelivery.locked = ["email.provider", "email.smtp.host"];

export const SMTPLockedByOperator: Story = {
  name: "SMTP, partly set by the operator",
  beforeEach: fakeBackend({ delivery: smtpDelivery, secrets: { smtpPassword: "secret" } }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByLabelText("SMTP host")).toBeDisabled());
    expect(canvas.getAllByText("Set by your platform operator.")).toHaveLength(2);
  },
};

const lockedVerification = defaultSettingsWire();
Object.assign(lockedVerification.security as Record<string, unknown>, {
  mfa: { mode: "required_for_roles", required_roles: ["admin"], max_assurance_age_hours: 8 },
  password_policy: { min_length: 16, enforcement: "require", grace_days: 30, changed_at: null },
  email_verification: { policy: "off", required: false },
});

export const StricterSecurity: Story = {
  name: "MFA for roles, required password change, verification off by platform",
  beforeEach: fakeBackend({ settings: lockedVerification }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByLabelText("Grace period (days)")).toBeInTheDocument());
    expect(canvas.getByText("Disabled by your platform configuration.")).toBeInTheDocument();
  },
};

export const RejectedField: Story = {
  name: "a rejected field",
  beforeEach: fakeBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const website = await canvas.findByLabelText("Website");
    await userEvent.clear(website);
    await userEvent.type(website, "ftp://acme.example");
    const general = canvas.getByRole("heading", { name: "General" }).closest("section") as HTMLElement;
    await userEvent.click(within(general).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(canvas.getByText("a website is an http or https URL")).toBeInTheDocument());
  },
};

export const LoadFailure: Story = {
  name: "load failure",
  beforeEach: fakeBackend({ failAll: true }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Couldn't load your settings.")).toBeInTheDocument());
  },
};
