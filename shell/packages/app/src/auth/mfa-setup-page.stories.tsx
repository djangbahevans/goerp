import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, within } from "storybook/test";
import { type MFASetupClient, MFASetupPage } from "./mfa-setup-page.js";

const USER = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada Lovelace",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: true,
  theme: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const TENANT = {
  id: "t1",
  slug: "acme",
  name: "Acme Corp",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
};

const AUTH: AuthContextValue = {
  state: { status: "authenticated", user: USER, tenant: TENANT },
  isAuthenticated: true,
  user: USER,
  tenant: TENANT,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

// A stand-in QR code: a 21×21 module grid, finder patterns plus a fixed
// scatter, so the layout reads like the real server-rendered SVG.
function fakeQRSvg(): string {
  const cells: string[] = [];
  const finder = (x: number, y: number) =>
    `<rect x="${x}" y="${y}" width="7" height="7"/><rect x="${x + 1}" y="${y + 1}" width="5" height="5" fill="#fff"/><rect x="${x + 2}" y="${y + 2}" width="3" height="3"/>`;
  for (let i = 0; i < 90; i++) {
    const x = (i * 7 + 3) % 21;
    const y = (i * 11 + 5) % 21;
    if ((x < 8 && y < 8) || (x > 12 && y < 8) || (x < 8 && y > 12)) continue;
    cells.push(`<rect x="${x}" y="${y}" width="1" height="1"/>`);
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 21 21" shape-rendering="crispEdges">${finder(0, 0)}${finder(14, 0)}${finder(0, 14)}${cells.join("")}</svg>`;
}

const ENROLLMENT = { enrollmentId: "e1", qrSvg: fakeQRSvg(), secret: "JBSWY3DPEHPK3PXPJBSWY3DP" };
const CODES = [
  "ABCDE-FGH23",
  "JKLMN-PQR45",
  "STUVW-XYZ67",
  "A2B3C-D4E5F",
  "G6H7J-K2L3M",
  "N4P5Q-R6S7T",
  "U2V3W-X4Y5Z",
  "BCDFG-HJKLM",
  "NPQRS-TVWXY",
  "Z2345-67ABC",
];

function client(overrides: Partial<MFASetupClient> = {}): MFASetupClient {
  return { begin: async () => ENROLLMENT, confirm: async () => CODES, ...overrides };
}

const withProviders: Decorator = (Story) => {
  const rootRoute = createRootRoute({
    component: () => (
      <AuthContext.Provider value={AUTH}>
        <Story />
      </AuthContext.Provider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/auth/mfa-setup"] }),
  });
  return <RouterProvider router={router} />;
};

const meta = {
  title: "Shell/Auth/MFASetupPage",
  component: MFASetupPage,
  args: { redirectTo: "/", client: client() },
  decorators: [withProviders],
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof MFASetupPage>;

export default meta;
type Story = StoryObj<typeof meta>;

// Step 1: scan the QR code or type the key, then enter a code.
export const ScanAndVerify: Story = {};

export const IncorrectCode: Story = {
  args: {
    client: client({
      confirm: async () => {
        throw new AppError({ code: "invalid_mfa_code", message: "invalid MFA code", httpStatus: 400 });
      },
    }),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.type(await canvas.findByLabelText("Verification code"), "000000");
    await expect(await canvas.findByText(/Incorrect code/)).toBeVisible();
  },
};

// Step 2: the recovery codes issued with the first factor.
export const RecoveryCodes: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.type(await canvas.findByLabelText("Verification code"), "123456");
    await expect(await canvas.findByRole("list", { name: "Recovery codes" })).toBeVisible();
    await expect(canvas.getByRole("button", { name: "Finish" })).toBeDisabled();
  },
};

export const StartFailed: Story = {
  args: {
    client: client({
      begin: async () => {
        throw new TypeError("Failed to fetch");
      },
    }),
  },
};
