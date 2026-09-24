import { Button, PasswordField, PasswordStrengthMeter } from "@goerp/sdk/components";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { type ReactNode, useState } from "react";
import { AuthLayout } from "./auth-layout.js";

const LOGO_SVG = `data:image/svg+xml;utf8,${encodeURIComponent(
  '<svg xmlns="http://www.w3.org/2000/svg" width="120" height="32" viewBox="0 0 120 32"><rect width="32" height="32" rx="4" fill="#2563eb"/><text x="40" y="22" font-family="sans-serif" font-size="16" font-weight="600" fill="#1f2937">Acme</text></svg>',
)}`;

function Field({ label, type = "text" }: { label: string; type?: string }): ReactNode {
  return (
    <label className="block text-sm text-text">
      {label}
      <input type={type} className="mt-1 w-full rounded-control border border-border px-3 py-2 text-sm" />
    </label>
  );
}

const signInContent = (
  <form className="space-y-4" onSubmit={(e) => e.preventDefault()}>
    <h1 className="text-xl font-semibold">Sign in</h1>
    <Field label="Email" type="email" />
    <Field label="Password" type="password" />
    <Button type="submit" variant="primary" fullWidth>
      Sign in
    </Button>
  </form>
);

function AcceptInviteContent(): ReactNode {
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  return (
    <form className="space-y-4" onSubmit={(e) => e.preventDefault()}>
      <h1 className="text-xl font-semibold">Join Acme Corp</h1>
      <p className="text-sm text-text-secondary">
        Jordan Lee invited you to join Acme Corp on GoERP. Set up your account below to accept the invitation and get
        started with your team.
      </p>
      <Field label="Email" type="email" />
      <Field label="Full name" />
      <Field label="Job title" />
      <Field label="Phone" />
      <PasswordField label="Password" value={password} onChange={setPassword} autoComplete="new-password" />
      <PasswordStrengthMeter password={password} />
      <PasswordField label="Confirm password" value={confirm} onChange={setConfirm} autoComplete="new-password" />
      <Button type="submit" variant="primary" fullWidth>
        Accept invitation
      </Button>
    </form>
  );
}

const meta: Meta<typeof AuthLayout> = {
  title: "Auth/AuthLayout",
  component: AuthLayout,
  parameters: { layout: "fullscreen" },
};

export default meta;

type Story = StoryObj<typeof AuthLayout>;

export const WithLogoAndPrivacyLink: Story = {
  args: {
    tenantLogo: { url: LOGO_SVG, alt: "Acme Corp" },
    privacyPolicyUrl: "https://example.com/privacy",
    children: signInContent,
  },
};

export const WithoutLogo: Story = {
  args: {
    privacyPolicyUrl: "https://example.com/privacy",
    children: signInContent,
  },
};

export const WithoutPrivacyLink: Story = {
  args: {
    tenantLogo: { url: LOGO_SVG, alt: "Acme Corp" },
    children: signInContent,
  },
};

export const TallContent: Story = {
  args: {
    tenantLogo: { url: LOGO_SVG, alt: "Acme Corp" },
    privacyPolicyUrl: "https://example.com/privacy",
    children: <AcceptInviteContent />,
  },
};
