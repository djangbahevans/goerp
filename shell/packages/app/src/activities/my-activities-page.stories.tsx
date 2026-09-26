import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { addDays, todayIn } from "./activity-dates.js";
import { type FakeActivitySeed, installFakeMyActivities } from "./fake-my-activities.js";
import { MyActivitiesPage } from "./my-activities-page.js";

const user = {
  id: "u1",
  email: "ama@acme.test",
  name: "Ama Owusu",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  theme: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const tenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
};
const auth: AuthContextValue = {
  state: { status: "authenticated", user, tenant },
  isAuthenticated: true,
  user,
  tenant,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};
const permissions = createPermissionContextValue({
  permissions: new Set<string>(),
  fieldAccess: {},
  modulesEnabled: new Set<string>(),
});

const withShell: Decorator = (Story) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={auth}>
          <PermissionContext.Provider value={permissions}>
            <Story />
          </PermissionContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  return <RouterProvider router={router} />;
};

const today = todayIn("UTC");

const MIXED: FakeActivitySeed[] = [
  { dueDate: addDays(today, -6), type: "email", summary: "Chase the overdue invoice", recordName: "Kofi Mensah" },
  { dueDate: addDays(today, -1), type: "todo", summary: "Update the contract draft", recordName: "SO-0042" },
  { dueDate: today, type: "meeting", summary: "Kick-off with the procurement team", recordName: "Abena Boateng" },
  { dueDate: today, type: "call", summary: "Confirm Friday delivery", recordName: "SO-0038" },
  { dueDate: addDays(today, 1), type: "email", summary: "Share the onboarding checklist", recordName: "Yaw Asante" },
  { dueDate: addDays(today, 9), type: "call", summary: "Quarterly check-in", recordName: null, recordId: "01j8x" },
];

const fake = (seeds: FakeActivitySeed[]) => () => installFakeMyActivities(seeds).restore;

const meta: Meta<typeof MyActivitiesPage> = {
  title: "Activities/My activities",
  component: MyActivitiesPage,
  decorators: [withShell],
  parameters: { layout: "fullscreen" },
};

export default meta;

type Story = StoryObj<typeof MyActivitiesPage>;

export const Grouped: Story = {
  name: "overdue, today and upcoming",
  beforeEach: fake(MIXED),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("region", { name: "Overdue" })).toBeInTheDocument());
    expect(canvas.getByRole("region", { name: "Today" })).toBeInTheDocument();
    expect(canvas.getByRole("region", { name: "Upcoming" })).toBeInTheDocument();
  },
};

export const MarkingDone: Story = {
  name: "marking one done",
  beforeEach: fake(MIXED),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: 'Mark "Confirm Friday delivery" done' }));
    await userEvent.type(
      canvas.getByRole("textbox", { name: 'Feedback for "Confirm Friday delivery"' }),
      "Customer confirmed Friday.",
    );
  },
};

export const Paged: Story = {
  name: "more than one page",
  beforeEach: fake(
    Array.from({ length: 60 }, (_, i) => ({
      dueDate: addDays(today, 2 + Math.floor(i / 6)),
      type: ["call", "meeting", "email", "todo"][i % 4] ?? "call",
      summary: `Follow-up task ${i + 1}`,
      recordName: `Contact ${i + 1}`,
    })),
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByRole("button", { name: "Load more" })).toBeInTheDocument());
  },
};

export const Empty: Story = {
  name: "nothing planned",
  beforeEach: fake([]),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Nothing planned")).toBeInTheDocument());
  },
};
