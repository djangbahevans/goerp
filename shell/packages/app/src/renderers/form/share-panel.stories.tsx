import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { SharePanel } from "./share-panel.js";

// SharePanel reads and writes through useShares, which goes through
// apiClient — the stories replace its three methods with an in-memory
// /_meta/shares for the duration of each story, so grant and revoke really
// round-trip without a backend.

interface WireShare {
  id: string;
  model: string;
  record_id: string;
  shared_with_user_id: string;
  shared_with_email: string;
  permission: "read" | "write";
  shared_by: string;
  created_at: string;
  expires_at?: string;
}

const FUTURE = new Date(Date.now() + 30 * 24 * 60 * 60 * 1000).toISOString();

const SEED: WireShare[] = [
  {
    id: "s3",
    model: "sales.order",
    record_id: "o1",
    shared_with_user_id: "u3",
    shared_with_email: "kwame.mensah@example.com",
    permission: "write",
    shared_by: "u1",
    created_at: "2026-09-19T10:00:00Z",
    expires_at: FUTURE,
  },
  {
    id: "s2",
    model: "sales.order",
    record_id: "o1",
    shared_with_user_id: "u2",
    shared_with_email: "ama.owusu@example.com",
    permission: "read",
    shared_by: "u1",
    created_at: "2026-09-18T10:00:00Z",
  },
  {
    id: "s1",
    model: "sales.order",
    record_id: "o1",
    shared_with_user_id: "u9",
    shared_with_email: "",
    permission: "read",
    shared_by: "u1",
    created_at: "2026-09-17T10:00:00Z",
  },
];

const KNOWN_EMAILS = new Set(["efua.boateng@example.com", "yaw.asante@example.com"]);

function fakeBackend(seed: WireShare[], options: { failLoad?: boolean } = {}) {
  return () => {
    const original = { get: apiClient.get, post: apiClient.post, delete: apiClient.delete };
    let shares = [...seed];
    let nextId = 100;
    const fake = apiClient as unknown as Record<string, unknown>;
    fake.get = async () => {
      if (options.failLoad) {
        throw new AppError({ code: "internal_error", message: "The share service is unavailable.", httpStatus: 500 });
      }
      return { data: shares };
    };
    fake.post = async (
      _path: string,
      body: { user_email: string; permission: "read" | "write"; expires_at?: string },
    ) => {
      if (!KNOWN_EMAILS.has(body.user_email.toLowerCase())) {
        throw new AppError({ code: "recipient_not_found", message: "no user with that email", httpStatus: 400 });
      }
      const created: WireShare = {
        id: `s${nextId++}`,
        model: "sales.order",
        record_id: "o1",
        shared_with_user_id: `u${nextId}`,
        shared_with_email: body.user_email.toLowerCase(),
        permission: body.permission,
        shared_by: "u1",
        created_at: new Date().toISOString(),
        ...(body.expires_at ? { expires_at: body.expires_at } : {}),
      };
      shares = [created, ...shares];
      return created;
    };
    fake.delete = async (path: string) => {
      const id = path.split("/").pop();
      shares = shares.filter((share) => share.id !== id);
    };
    return () => {
      for (const [key, fn] of Object.entries(original)) fake[key] = fn;
    };
  };
}

// A fresh QueryClient per story so one story's cached shares can't leak into the next.
const withPanelFrame: Decorator = (Story) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <div className="w-90 max-w-[calc(100vw-2rem)] rounded-structural border border-border bg-surface p-3 text-sm shadow-md">
      <Story />
    </div>
  </QueryClientProvider>
);

const meta: Meta<typeof SharePanel> = {
  title: "Renderers/SharePanel",
  component: SharePanel,
  decorators: [withPanelFrame],
  args: {
    resource: "sales.order",
    recordId: "o1",
    label: "Order",
    permissions: ["read", "write"],
    headingId: "share-panel-heading",
  },
};

export default meta;

type Story = StoryObj<typeof SharePanel>;

export const WithShares: Story = {
  name: "list: badges, expiry, unknown recipient",
  beforeEach: fakeBackend(SEED),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("kwame.mensah@example.com")).toBeInTheDocument());
    const list = within(canvas.getByRole("list"));
    expect(list.getByText("ama.owusu@example.com")).toBeInTheDocument();
    expect(list.getByText("Unknown user")).toBeInTheDocument();
    expect(list.getByText("Can edit")).toBeInTheDocument();
    expect(list.getAllByText("Can view")).toHaveLength(2);
    expect(list.getAllByText(/^Expires /)).toHaveLength(1);
    expect(canvas.getByLabelText("Email")).toHaveFocus();
  },
};

export const Empty: Story = {
  name: "empty: not shared yet",
  beforeEach: fakeBackend([]),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("Not shared with anyone yet.")).toBeInTheDocument());
  },
};

export const LoadError: Story = {
  name: "load error, with a working Retry",
  beforeEach: fakeBackend(SEED, { failLoad: true }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const alert = await waitFor(() => canvas.getByRole("alert"));
    await expect(within(alert).getByText("The share service is unavailable.")).toBeInTheDocument();
    await expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
    // The grant form stays usable while the list is unavailable.
    expect(canvas.getByLabelText("Email")).toBeEnabled();
  },
};

export const GrantAndRevoke: Story = {
  name: "grant a share, see it listed, then revoke it",
  beforeEach: fakeBackend(SEED),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("ama.owusu@example.com")).toBeInTheDocument());

    await userEvent.type(canvas.getByLabelText("Email"), "Efua.Boateng@example.com");
    await userEvent.click(canvas.getByLabelText("Can edit"));
    await userEvent.click(canvas.getByRole("button", { name: "Share" }));

    await waitFor(() => expect(canvas.getByText("efua.boateng@example.com")).toBeInTheDocument());
    expect(canvas.getByRole("status")).toHaveTextContent("Shared with Efua.Boateng@example.com.");
    expect(canvas.getByLabelText("Email")).toHaveValue("");
    expect(canvas.getByLabelText("Email")).toHaveFocus();
    expect(canvas.getByLabelText("Can edit")).toBeChecked();

    await userEvent.click(canvas.getByRole("button", { name: "Revoke access for efua.boateng@example.com" }));
    await waitFor(() => expect(canvas.queryByText("efua.boateng@example.com")).not.toBeInTheDocument());
    expect(canvas.getByText("ama.owusu@example.com")).toBeInTheDocument();
  },
};

export const RecipientNotFound: Story = {
  name: "grant errors: unknown email, empty email, already shared",
  beforeEach: fakeBackend(SEED),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("ama.owusu@example.com")).toBeInTheDocument());

    await userEvent.click(canvas.getByRole("button", { name: "Share" }));
    expect(canvas.getByText("Enter an email address.")).toBeInTheDocument();

    await userEvent.type(canvas.getByLabelText("Email"), "nobody@example.com{Enter}");
    await waitFor(() => expect(canvas.getByText("No user with that email address.")).toBeInTheDocument());

    await userEvent.clear(canvas.getByLabelText("Email"));
    await userEvent.type(canvas.getByLabelText("Email"), "AMA.OWUSU@example.com{Enter}");
    expect(canvas.getByText(/Already shared with this user/)).toBeInTheDocument();
  },
};

export const SingleLevel: Story = {
  name: "a model that accepts only read: no Access control",
  args: { permissions: ["read"] },
  beforeEach: fakeBackend(SEED),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("They'll be able to view this Order.")).toBeInTheDocument());
    expect(canvas.queryByRole("radiogroup")).not.toBeInTheDocument();
  },
};

export const NoLevels: Story = {
  name: "a model that accepts no level: the form is replaced by a notice",
  args: { permissions: [] },
  beforeEach: fakeBackend([]),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Sharing isn't enabled for Order records.")).toBeInTheDocument());
    expect(canvas.queryByLabelText("Email")).not.toBeInTheDocument();
  },
};
