import { apiClient } from "@goerp/sdk";
import { AuthContext, type AuthContextValue } from "@goerp/sdk/auth";
import { FieldWrapper } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import type { RecordReader } from "@goerp/sdk/react";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { RecordReaderPicker } from "./record-reader-picker.js";

// useRecordReaders goes through apiClient, so each story swaps apiClient.get
// for an in-memory GET /_meta/record-readers.

interface WireReader {
  id: string;
  name: string | null;
  email: string;
  avatar_url: string | null;
}

const READERS: WireReader[] = [
  { id: "u1", name: "Ama Owusu", email: "ama@acme.example", avatar_url: null },
  { id: "u4", name: "Ama Boateng", email: "ama.boateng@acme.example", avatar_url: null },
  { id: "u2", name: "Kwame Mensah", email: "kwame@acme.example", avatar_url: null },
  { id: "u3", name: null, email: "ops@acme.example", avatar_url: null },
];

function fakeReaders(options: { fail?: boolean; delayMs?: number } = {}) {
  return () => {
    const original = apiClient.get;
    const fake = apiClient as unknown as Record<string, unknown>;
    fake.get = async (_path: string, config?: { params?: { q?: string } }) => {
      if (options.delayMs) await new Promise((resolve) => setTimeout(resolve, options.delayMs));
      if (options.fail) {
        throw new AppError({ code: "internal_error", message: "unavailable", httpStatus: 500 });
      }
      const q = (config?.params?.q ?? "").toLowerCase();
      const data = READERS.filter(
        (r) =>
          q === "" ||
          r.email.toLowerCase().startsWith(q) ||
          (r.name ?? "")
            .toLowerCase()
            .split(" ")
            .some((word) => word.startsWith(q)),
      );
      return { data };
    };
    return () => {
      fake.get = original;
    };
  };
}

const auth = { user: { id: "u1" } } as unknown as AuthContextValue;

const withFrame: Decorator = (Story) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={client}>
      <AuthContext.Provider value={auth}>
        <div className="max-w-sm">
          <Story />
        </div>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
};

function Assignee({ initial = null, clearable }: { initial?: RecordReader | null; clearable?: boolean }) {
  const [value, setValue] = useState<RecordReader | null>(initial);
  return (
    <FieldWrapper label="Assignee">
      <RecordReaderPicker model="sales.order" recordId="o1" value={value} onChange={setValue} clearable={clearable} />
    </FieldWrapper>
  );
}

const meta: Meta<typeof Assignee> = {
  title: "Renderers/RecordReaderPicker",
  component: Assignee,
  decorators: [withFrame],
};

export default meta;

type Story = StoryObj<typeof Assignee>;

const body = within(document.body);

export const Open: Story = {
  name: "open: viewer marked, same names told apart by email",
  beforeEach: fakeReaders(),
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("combobox", { name: "Assignee" }));
    await waitFor(() =>
      expect(body.getByRole("option", { name: "Ama Owusu (you), ama@acme.example" })).toBeInTheDocument(),
    );
    expect(body.getByRole("option", { name: "Ama Boateng, ama.boateng@acme.example" })).toBeInTheDocument();
    expect(body.getByRole("option", { name: "ops@acme.example" })).toBeInTheDocument();
  },
};

export const Search: Story = {
  name: "search and pick",
  beforeEach: fakeReaders(),
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole("combobox", { name: "Assignee" });
    await userEvent.type(input, "kw");
    await waitFor(() => expect(body.getAllByRole("option")).toHaveLength(1));
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(input).toHaveValue("Kwame Mensah"));
  },
};

export const WithValue: Story = {
  name: "closed with a value, not clearable",
  beforeEach: fakeReaders(),
  args: {
    initial: { id: "u2", name: "Kwame Mensah", email: "kwame@acme.example", avatarUrl: null },
    clearable: false,
  },
};

export const NoResults: Story = {
  name: "no one found",
  beforeEach: fakeReaders(),
  play: async ({ canvasElement }) => {
    await userEvent.type(within(canvasElement).getByRole("combobox", { name: "Assignee" }), "zz");
    await waitFor(() => expect(body.getByText("No one found who can see this record.")).toBeInTheDocument());
  },
};

export const Loading: Story = {
  name: "first load",
  beforeEach: fakeReaders({ delayMs: 60_000 }),
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("combobox", { name: "Assignee" }));
    await waitFor(() => expect(document.querySelector("[data-skeleton='lines']")).toBeInTheDocument());
  },
};

export const Failed: Story = {
  name: "search failed",
  beforeEach: fakeReaders({ fail: true }),
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("combobox", { name: "Assignee" }));
    await waitFor(() => expect(body.getByText("Couldn't load people.")).toBeInTheDocument());
  },
};

export const InModal: Story = {
  name: "inside a dialog",
  beforeEach: fakeReaders(),
  // useFloatingPanelLayer only checks for a dialog ancestor, so a bare role="dialog" stands in for a modal.
  render: () => (
    <div role="dialog" aria-label="Schedule activity" className="rounded-structural border border-border p-4">
      <Assignee />
    </div>
  ),
  play: async () => {
    await userEvent.click(body.getByRole("combobox", { name: "Assignee" }));
    await waitFor(() => expect(body.getByRole("listbox").className).toContain("z-(--z-modal)"));
  },
};
