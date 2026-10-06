import type { ActiveSession, AuthContextValue, MFAFactor, MFAFactors } from "@goerp/sdk/auth";
import { AuthContext } from "@goerp/sdk/auth";
import { Toast } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { SecurityPage, type SessionsClient } from "./security-page.js";
import type { TwoFactorClient } from "./two-factor-section.js";

function ago(minutes: number): string {
  return new Date(Date.now() - minutes * 60_000).toISOString();
}

const THIS_DEVICE: ActiveSession = {
  id: "fam-this",
  userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/537.36 Chrome/128.0.0.0 Safari/537.36",
  ipAddress: "41.66.18.2",
  countryCode: "GH",
  signedInAt: ago(60 * 24 * 3),
  lastActiveAt: ago(0),
  persistent: true,
  current: true,
};

const OTHER_SESSIONS: ActiveSession[] = [
  {
    id: "fam-phone",
    userAgent:
      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 Version/17.5 Safari/605.1.15",
    ipAddress: "102.176.4.9",
    countryCode: "GH",
    signedInAt: ago(60 * 24 * 12),
    lastActiveAt: ago(45),
    persistent: true,
    current: false,
  },
  {
    id: "fam-office",
    userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Gecko/20100101 Firefox/130.0",
    ipAddress: "185.23.91.14",
    countryCode: "GB",
    signedInAt: ago(60 * 24 * 20),
    lastActiveAt: ago(60 * 26),
    persistent: false,
    current: false,
  },
  {
    id: "fam-unknown",
    userAgent: null,
    ipAddress: null,
    countryCode: null,
    signedInAt: ago(60 * 24 * 30),
    lastActiveAt: ago(60 * 24 * 6),
    persistent: true,
    current: false,
  },
];

// An in-memory client, so Sign out really removes the session and a
// refetch reflects it.
function memoryClient(initial: ActiveSession[]): SessionsClient {
  let sessions = [...initial];
  return {
    list: async () => sessions,
    revoke: async (id) => {
      sessions = sessions.filter((s) => s.id !== id);
    },
    revokeOthers: async () => {
      const revoked = sessions.filter((s) => !s.current).length;
      sessions = sessions.filter((s) => s.current);
      return revoked;
    },
  };
}

const VALID_CODE = "123456";

const IPHONE: MFAFactor = {
  tenantOnly: false,
  id: "f-iphone",
  type: "totp",
  label: "iPhone",
  createdAt: ago(60 * 24 * 40),
  lastUsedAt: ago(60 * 5),
};

const WORK_PHONE: MFAFactor = {
  tenantOnly: true,
  id: "f-work",
  type: "totp",
  label: null,
  createdAt: ago(60 * 24 * 3),
  lastUsedAt: null,
};

const QR_SVG =
  "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 21 21'><rect width='21' height='21' fill='#fff'/><path d='M0 0h7v7H0zM14 0h7v7h-7zM0 14h7v7H0zM9 9h3v3H9zM15 15h2v2h-2z' fill='#000'/></svg>";

function codeError(): AppError {
  return new AppError({ code: "invalid_mfa_code", message: "invalid MFA code", httpStatus: 401 });
}

// An in-memory 2FA backend. Only VALID_CODE, or a stored recovery code,
// passes.
function memoryTwoFactor(initial: Partial<MFAFactors> = {}): TwoFactorClient {
  let state: MFAFactors = { factors: [], recoveryCodesRemaining: 0, requiredByPolicy: false, ...initial };
  const check = (code: string) => {
    if (code !== VALID_CODE) throw codeError();
  };
  return {
    list: async () => state,
    remove: async (id, { code }) => {
      check(code);
      state = { ...state, factors: state.factors.filter((f) => f.id !== id) };
    },
    regenerate: async ({ code }) => {
      check(code);
      const codes = Array.from({ length: 10 }, (_, i) => `K${i}Q7M-${"XR4PT".slice(0, 5 - String(i).length)}${i}`);
      state = { ...state, recoveryCodesRemaining: codes.length };
      return codes;
    },
    begin: async () => ({ enrollmentId: "enr-1", qrSvg: QR_SVG, secret: "JBSWY3DPEHPK3PXP" }),
    confirm: async ({ code }) => {
      check(code);
      return null;
    },
    reverify: async (confirmation) => {
      if (confirmation.type !== "webauthn") check(confirmation.code);
    },
  };
}

const fakeUser = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada Lovelace",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: ["pwd", "totp"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  passwordChangeRequired: false,
  passwordMinLength: 12,
  phone: null,
  title: null,
  theme: "system" as const,
  contrast: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const fakeTenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
  passwordMinLength: 12,
};
const AUTH: AuthContextValue = {
  state: { status: "authenticated", user: fakeUser, tenant: fakeTenant },
  isAuthenticated: true,
  user: fakeUser,
  tenant: fakeTenant,
  login: async () => null,
  completeHandoff: async () => {},
  selectTenant: async () => null,
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
  expireSession: () => {},
};

// A router for the Password link and a fresh query client per story, so
// one story's cached sessions can't leak into the next.
const withShell: Decorator = (Story) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <AuthContext.Provider value={AUTH}>
        <QueryClientProvider client={queryClient}>
          <Story />
          <Toast />
        </QueryClientProvider>
      </AuthContext.Provider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/settings/security"] }),
  });
  return <RouterProvider router={router} />;
};

const ONE_FACTOR: Partial<MFAFactors> = { factors: [IPHONE], recoveryCodesRemaining: 7 };

const meta: Meta<typeof SecurityPage> = {
  title: "Shell/Settings/SecurityPage",
  component: SecurityPage,
  decorators: [withShell],
};

export default meta;
type Story = StoryObj<typeof meta>;

export const SeveralSessions: Story = {
  render: () => (
    <SecurityPage
      client={memoryClient([THIS_DEVICE, ...OTHER_SESSIONS])}
      twoFactorClient={memoryTwoFactor(ONE_FACTOR)}
    />
  ),
};

export const OnlyThisDevice: Story = {
  render: () => <SecurityPage client={memoryClient([THIS_DEVICE])} twoFactorClient={memoryTwoFactor(ONE_FACTOR)} />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByText("You're not signed in anywhere else.")).toBeInTheDocument();
    await expect(canvas.queryByRole("button", { name: "Sign out all other sessions" })).toBeNull();
  },
};

export const Loading: Story = {
  render: () => (
    <SecurityPage
      client={{
        ...memoryClient([]),
        list: () => new Promise<ActiveSession[]>(() => {}),
      }}
      twoFactorClient={{ ...memoryTwoFactor(), list: () => new Promise<MFAFactors>(() => {}) }}
    />
  ),
};

export const LoadError: Story = {
  render: () => (
    <SecurityPage
      client={{
        ...memoryClient([]),
        list: async () => {
          throw new AppError({ code: "internal_error", message: "list sessions failed", httpStatus: 500 });
        },
      }}
      twoFactorClient={memoryTwoFactor(ONE_FACTOR)}
    />
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByText("Couldn't load your sessions.")).toBeInTheDocument();
  },
};

export const SignOutOneSession: Story = {
  render: () => (
    <SecurityPage
      client={memoryClient([THIS_DEVICE, ...OTHER_SESSIONS])}
      twoFactorClient={memoryTwoFactor(ONE_FACTOR)}
    />
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    const [firstSignOut] = await canvas.findAllByRole("button", { name: "Sign out" });
    await userEvent.click(firstSignOut as HTMLElement);
    await userEvent.click(within(await body.findByRole("alertdialog")).getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(canvas.queryByText("Safari on iOS")).toBeNull());
  },
};

export const SignOutAllOthers: Story = {
  render: () => (
    <SecurityPage
      client={memoryClient([THIS_DEVICE, ...OTHER_SESSIONS])}
      twoFactorClient={memoryTwoFactor(ONE_FACTOR)}
    />
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(await canvas.findByRole("button", { name: "Sign out all other sessions" }));
    await userEvent.click(within(await body.findByRole("alertdialog")).getByRole("button", { name: "Sign out all" }));
    await expect(await body.findByText("Signed out of 3 other sessions")).toBeInTheDocument();
    await expect(await canvas.findByText("You're not signed in anywhere else.")).toBeInTheDocument();
  },
};

export const TwoFactorOff: Story = {
  render: () => <SecurityPage client={memoryClient([THIS_DEVICE])} twoFactorClient={memoryTwoFactor()} />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByText("Off")).toBeInTheDocument();
    await expect(canvas.queryByText(/recovery codes? left/)).toBeNull();
  },
};

export const TwoFactorOneFactor: Story = {
  render: () => <SecurityPage client={memoryClient([THIS_DEVICE])} twoFactorClient={memoryTwoFactor(ONE_FACTOR)} />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByRole("button", { name: "Remove iPhone" })).toBeInTheDocument();
    await expect(canvas.getByText(/7 recovery codes left/)).toBeInTheDocument();
  },
};

export const TwoFactorTwoFactors: Story = {
  render: () => (
    <SecurityPage
      client={memoryClient([THIS_DEVICE])}
      twoFactorClient={memoryTwoFactor({ factors: [IPHONE, WORK_PHONE], recoveryCodesRemaining: 2 })}
    />
  ),
  play: async ({ canvasElement }) => {
    await expect(await within(canvasElement).findByText("Only for Acme")).toBeInTheDocument();
  },
};

export const TwoFactorLastFactorRequiredByPolicy: Story = {
  render: () => (
    <SecurityPage
      client={memoryClient([THIS_DEVICE])}
      twoFactorClient={memoryTwoFactor({ ...ONE_FACTOR, requiredByPolicy: true })}
    />
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByText("Your organisation requires two-factor authentication")).toBeInTheDocument();
    await expect(canvas.queryByRole("button", { name: /Remove/ })).toBeNull();
  },
};

const renderTwoFactors = () => (
  <SecurityPage
    client={memoryClient([THIS_DEVICE])}
    twoFactorClient={memoryTwoFactor({ factors: [IPHONE, WORK_PHONE], recoveryCodesRemaining: 7 })}
  />
);

export const TwoFactorRemoveDialog: Story = {
  render: renderTwoFactors,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(await canvas.findByRole("button", { name: "Remove iPhone" }));
    const dialog = within(await body.findByRole("alertdialog"));
    await expect(dialog.getByText(/signed out everywhere, including this browser/)).toBeInTheDocument();
  },
};

export const TwoFactorRemoveTenantOnlyDialog: Story = {
  render: renderTwoFactors,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(await canvas.findByRole("button", { name: "Remove authenticator app" }));
    const dialog = within(await body.findByRole("alertdialog"));
    await expect(dialog.getByText(/signed out of Acme, including this browser/)).toBeInTheDocument();
  },
};

export const TwoFactorRemoveInvalidCode: Story = {
  render: renderTwoFactors,
  play: async (context) => {
    await TwoFactorRemoveDialog.play?.(context);
    const body = within(context.canvasElement.ownerDocument.body);
    const dialog = within(await body.findByRole("alertdialog"));
    await userEvent.type(dialog.getByLabelText("Verification code"), "000000");
    await expect(
      await dialog.findByText("Incorrect code. Check your authenticator app and try again."),
    ).toBeInTheDocument();
  },
};

export const TwoFactorRegeneratedCodes: Story = {
  render: () => <SecurityPage client={memoryClient([THIS_DEVICE])} twoFactorClient={memoryTwoFactor(ONE_FACTOR)} />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(await canvas.findByRole("button", { name: "Generate new codes" }));
    const dialog = within(await body.findByRole("alertdialog"));
    await userEvent.type(dialog.getByLabelText("Verification code"), VALID_CODE);
    const list = await dialog.findByRole("list", { name: "Recovery codes" });
    await expect(within(list).getAllByRole("listitem")).toHaveLength(10);
  },
};

export const TwoFactorAddAuthenticator: Story = {
  render: () => <SecurityPage client={memoryClient([THIS_DEVICE])} twoFactorClient={memoryTwoFactor(ONE_FACTOR)} />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(await canvas.findByRole("button", { name: "Add authenticator app" }));
    const sheet = within(await body.findByRole("dialog", { name: "Add authenticator app" }));
    await expect(await sheet.findByAltText(/QR code/)).toBeInTheDocument();
  },
};
