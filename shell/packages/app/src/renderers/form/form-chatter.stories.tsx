import { apiClient } from "@goerp/sdk";
import { AuthContext, type AuthContextValue } from "@goerp/sdk/auth";
import { ConfirmDialogHost } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { FormChatter } from "./form-chatter.js";
import type { FormViewDeclaration } from "./form-view-types.js";

// FormChatter reads and writes through useRecordActivity, which goes through
// apiClient — the stories replace its methods with an in-memory
// /_meta/activity. The model schema and relation labels are seeded into the
// QueryClient, since Storybook has no GET /_meta/schema to resolve them from.

interface WireEntry {
  id: string;
  kind: "created" | "change" | "comment" | "activity_done";
  body?: string;
  deleted?: boolean;
  changes?: { field: string; old: unknown; new: unknown }[];
  activity?: { activity_id: string; type: string; summary: string; due_date: string; feedback: string | null };
  author: { id: string; name: string | null; avatar_url: string | null } | null;
  created_at: string;
}

const AMA = { id: "u1", name: "Ama Owusu", avatar_url: null };
const KWAME = { id: "u2", name: "Kwame Mensah", avatar_url: null };

const SEED: WireEntry[] = [
  {
    id: "e8",
    kind: "comment",
    body: "Customer asked to move delivery to Friday.\nConfirmed by phone.",
    deleted: false,
    author: AMA,
    created_at: "2026-09-24T16:02:00Z",
  },
  {
    id: "e7",
    kind: "change",
    changes: [{ field: "state", old: "draft", new: "confirmed" }],
    author: KWAME,
    created_at: "2026-09-24T10:15:00Z",
  },
  {
    id: "e6",
    kind: "comment",
    body: "Is the discount approved?",
    deleted: false,
    author: KWAME,
    created_at: "2026-09-23T15:00:00Z",
  },
  { id: "e5", kind: "comment", deleted: true, author: AMA, created_at: "2026-09-23T14:00:00Z" },
  {
    id: "e4",
    kind: "activity_done",
    activity: {
      activity_id: "a1",
      type: "call",
      summary: "Confirm delivery address",
      due_date: "2026-09-22",
      feedback: "Address confirmed; gate code added to the notes.",
    },
    author: AMA,
    created_at: "2026-09-22T09:30:00Z",
  },
  {
    id: "e3",
    kind: "change",
    changes: [
      { field: "customer_id", old: "c1", new: "c2" },
      { field: "delivery_on", old: "2026-09-25", new: "2026-09-26" },
    ],
    author: null,
    created_at: "2026-09-21T02:00:00Z",
  },
  {
    id: "e2",
    kind: "comment",
    body: "Drafted from the phone order.",
    deleted: false,
    author: KWAME,
    created_at: "2026-09-20T11:00:00Z",
  },
  { id: "e1", kind: "created", author: KWAME, created_at: "2026-09-20T10:59:00Z" },
];

const PAGE_SIZE = 5;

interface WirePlanned {
  id: string;
  model: string;
  record_id: string;
  type: string;
  summary: string;
  note: string | null;
  due_date: string;
  assignee: { id: string; name: string | null; avatar_url: string | null };
  created_by: { id: string; name: string | null; avatar_url: string | null };
  created_at: string;
  done_at: string | null;
  done_by: null;
  feedback: string | null;
}

const ISO_DAY = 86_400_000;
const dayFromToday = (offset: number) => new Date(Date.now() + offset * ISO_DAY).toISOString().slice(0, 10);

function planned(id: string, overrides: Partial<WirePlanned>): WirePlanned {
  return {
    id,
    model: "sales.order",
    record_id: "o1",
    type: "call",
    summary: "",
    note: null,
    due_date: dayFromToday(0),
    assignee: AMA,
    created_by: KWAME,
    created_at: "2026-09-23T16:02:00Z",
    done_at: null,
    done_by: null,
    feedback: null,
    ...overrides,
  };
}

const PLANNED_SEED: WirePlanned[] = [
  planned("p1", { summary: "Chase the signed delivery note", type: "todo", due_date: dayFromToday(-2) }),
  planned("p2", {
    summary: "Confirm Friday delivery",
    note: "They asked for a morning slot.",
    due_date: dayFromToday(0),
  }),
  planned("p3", { summary: "Send the revised quote", type: "email", assignee: KWAME, due_date: dayFromToday(3) }),
];

const READERS = [
  { id: "u1", name: "Ama Owusu", email: "ama@acme.example", avatar_url: null },
  { id: "u2", name: "Kwame Mensah", email: "kwame@acme.example", avatar_url: null },
];

function fakeBackend(
  seed: WireEntry[],
  options: { failLoad?: boolean; failPost?: boolean; planned?: WirePlanned[] } = {},
) {
  return () => {
    const original = { get: apiClient.get, post: apiClient.post, patch: apiClient.patch, delete: apiClient.delete };
    let entries = [...seed];
    let plannedRows = [...(options.planned ?? [])];
    let nextId = 100;
    const fake = apiClient as unknown as Record<string, unknown>;
    fake.get = async (path: string, config?: { params?: { cursor?: string; q?: string } }) => {
      if (path === "/_meta/scheduled-activities") return { data: plannedRows };
      if (path === "/_meta/record-readers") {
        const q = (config?.params?.q ?? "").toLowerCase();
        return { data: READERS.filter((r) => q === "" || r.name.toLowerCase().includes(q)) };
      }
      if (options.failLoad) {
        throw new AppError({
          code: "internal_error",
          message: "The activity service is unavailable.",
          httpStatus: 500,
        });
      }
      const start = config?.params?.cursor ? Number(config.params.cursor) : 0;
      const page = entries.slice(start, start + PAGE_SIZE);
      const hasMore = start + PAGE_SIZE < entries.length;
      return { data: page, meta: { cursor: hasMore ? String(start + PAGE_SIZE) : "", has_more: hasMore } };
    };
    fake.post = async (path: string, body: Record<string, unknown>) => {
      if (path === "/_meta/scheduled-activities") {
        const assignee = READERS.find((r) => r.id === (body.assignee_id ?? "u1")) ?? READERS[0];
        const created = planned(`p${nextId++}`, {
          type: String(body.type),
          summary: String(body.summary),
          note: (body.note as string | undefined) ?? null,
          due_date: String(body.due_date),
          assignee: { id: assignee?.id ?? "u1", name: assignee?.name ?? null, avatar_url: null },
          created_by: AMA,
        });
        plannedRows = [...plannedRows, created].sort((a, b) => a.due_date.localeCompare(b.due_date));
        return created;
      }
      if (path.endsWith("/done")) {
        const id = path.split("/").at(-2);
        const done = plannedRows.find((row) => row.id === id);
        plannedRows = plannedRows.filter((row) => row.id !== id);
        if (done) {
          const feedback = (body.feedback as string | undefined) ?? null;
          entries = [
            {
              id: `e${nextId++}`,
              kind: "activity_done",
              activity: {
                activity_id: done.id,
                type: done.type,
                summary: done.summary,
                due_date: done.due_date,
                feedback,
              },
              author: AMA,
              created_at: new Date().toISOString(),
            },
            ...entries,
          ];
          return { ...done, done_at: new Date().toISOString(), feedback };
        }
        throw new AppError({ code: "not_found", message: "Not found", httpStatus: 404 });
      }
      if (options.failPost) {
        throw new AppError({
          code: "internal_error",
          message: "Couldn't reach the server. Try again.",
          httpStatus: 503,
        });
      }
      const created: WireEntry = {
        id: `e${nextId++}`,
        kind: "comment",
        body: String(body.body).trim(),
        deleted: false,
        author: AMA,
        created_at: new Date().toISOString(),
      };
      entries = [created, ...entries];
      return created;
    };
    fake.patch = async (path: string, body: Record<string, unknown>) => {
      const id = path.split("/").pop();
      let updated: WirePlanned | undefined;
      plannedRows = plannedRows.map((row) => {
        if (row.id !== id) return row;
        updated = {
          ...row,
          ...(body.type !== undefined ? { type: String(body.type) } : {}),
          ...(body.summary !== undefined ? { summary: String(body.summary) } : {}),
          ...(body.due_date !== undefined ? { due_date: String(body.due_date) } : {}),
          ...(body.note !== undefined ? { note: body.note as string | null } : {}),
        };
        return updated;
      });
      return updated;
    };
    fake.delete = async (path: string) => {
      const id = path.split("/").pop();
      if (path.startsWith("/_meta/scheduled-activities/")) {
        plannedRows = plannedRows.filter((row) => row.id !== id);
        return;
      }
      entries = entries.map((entry) => {
        if (entry.id !== id) return entry;
        const { body: _body, ...rest } = entry;
        return { ...rest, deleted: true };
      });
    };
    return () => {
      for (const [key, fn] of Object.entries(original)) fake[key] = fn;
    };
  };
}

const view: FormViewDeclaration = {
  name: "orders_form",
  type: "form",
  resource: "sales.order",
  label: "Order",
  sections: [
    {
      fields: [
        {
          field: "state",
          label: "Status",
          options: [
            { value: "draft", label: "Draft" },
            { value: "confirmed", label: "Confirmed" },
          ],
        },
        { field: "customer_id", label: "Customer" },
      ],
    },
  ],
};

const auth = { user: { id: "u1" } } as unknown as AuthContextValue;

// A fresh QueryClient per story so one story's cached feed can't leak into the next.
const withChatterFrame: Decorator = (Story) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["form-chatter-model", "sales.order"], {
    name: "sales.order",
    label: "Order",
    label_plural: "Orders",
    enabled_ops: [],
    shareable: false,
    fields: [
      { name: "state", type: "selection" },
      { name: "customer_id", type: "many2one", related_model: "contacts.contact" },
      { name: "delivery_on", type: "date" },
    ],
  });
  client.setQueryData(["relation-labels", "contacts.contact", "", "", ["c1", "c2"]], {
    c1: "Globex Inc",
    c2: "Acme Corp",
  });
  return (
    <QueryClientProvider client={client}>
      <AuthContext.Provider value={auth}>
        <div className="max-w-3xl">
          <Story />
        </div>
        <ConfirmDialogHost />
      </AuthContext.Provider>
    </QueryClientProvider>
  );
};

const meta: Meta<typeof FormChatter> = {
  title: "Renderers/FormChatter",
  component: FormChatter,
  decorators: [withChatterFrame],
  args: { view, recordId: "o1" },
};

export default meta;

type Story = StoryObj<typeof FormChatter>;

export const Feed: Story = {
  name: "feed: every entry kind, load more",
  beforeEach: fakeBackend(SEED),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Changed Status")).toBeInTheDocument());
    expect(canvas.getByText("Comment deleted")).toBeInTheDocument();
    expect(canvas.getAllByRole("button", { name: /^Delete comment from / })).toHaveLength(1);

    await userEvent.click(canvas.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(canvas.getByText("Created this record")).toBeInTheDocument());
    expect(canvas.getByText("Changed 2 fields by the system")).toBeInTheDocument();
    expect(canvas.queryByRole("button", { name: "Load more" })).toBeNull();
    expect(canvas.getByRole("status")).toHaveTextContent("3 more entries loaded");
  },
};

export const Empty: Story = {
  name: "empty: no activity yet",
  beforeEach: fakeBackend([]),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("No activity yet")).toBeInTheDocument());
  },
};

export const LoadError: Story = {
  name: "load error, composer still usable",
  beforeEach: fakeBackend(SEED, { failLoad: true }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const alert = await waitFor(() => canvas.getByRole("alert"));
    await expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(canvas.getByLabelText("Add a comment")).not.toHaveAttribute("readonly");
  },
};

export const Unsaved: Story = {
  name: "unsaved record",
  args: { recordId: undefined },
};

export const PostAndDelete: Story = {
  name: "post a comment, then delete it",
  beforeEach: fakeBackend(SEED),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    await waitFor(() => expect(canvas.getByText("Changed Status")).toBeInTheDocument());

    await userEvent.type(canvas.getByLabelText("Add a comment"), "Delivery booked for Friday.");
    await userEvent.click(canvas.getByRole("button", { name: "Comment" }));
    await waitFor(() => expect(canvas.getByText("Delivery booked for Friday.")).toBeInTheDocument());
    expect(canvas.getByLabelText("Add a comment")).toHaveValue("");
    expect(canvas.getByRole("status")).toHaveTextContent("Comment posted");

    const [newest] = canvas.getAllByRole("button", { name: /^Delete comment from / });
    if (!newest) throw new Error("expected a delete button on the new comment");
    await userEvent.click(newest);
    await userEvent.click(await body.findByRole("button", { name: "Delete" }));
    await waitFor(() => expect(canvas.queryByText("Delivery booked for Friday.")).toBeNull());
    await waitFor(() => expect(canvasElement.ownerDocument.activeElement).toHaveTextContent("Comment deleted"));
  },
};

export const PostError: Story = {
  name: "post error keeps the text",
  beforeEach: fakeBackend(SEED, { failPost: true }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.type(canvas.getByLabelText("Add a comment"), "This will fail");
    await userEvent.click(canvas.getByRole("button", { name: "Comment" }));
    await waitFor(() => expect(canvas.getByRole("alert")).toHaveTextContent("Couldn't reach the server. Try again."));
    expect(canvas.getByLabelText("Add a comment")).toHaveValue("This will fail");
  },
};

export const PlannedActivities: Story = {
  name: "planned activities: overdue marked, mark one done into the feed",
  beforeEach: fakeBackend(SEED, { planned: PLANNED_SEED }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const section = await waitFor(() => canvas.getByRole("region", { name: "Planned activities" }));
    await waitFor(() => expect(within(section).getByText("Chase the signed delivery note")).toBeInTheDocument());
    expect(within(section).getByText(/^Overdue · /)).toBeInTheDocument();
    // Kwame's quote is assigned to him and made by him, so the viewer (Ama) gets no actions on it.
    expect(within(section).queryByRole("button", { name: 'Mark "Send the revised quote" done' })).toBeNull();

    await userEvent.click(within(section).getByRole("button", { name: 'Mark "Confirm Friday delivery" done' }));
    await userEvent.type(
      within(section).getByRole("textbox", { name: /^Feedback for/ }),
      "Morning slot booked.{Enter}",
    );
    await waitFor(() => expect(within(section).queryByText("Confirm Friday delivery")).toBeNull());
    await waitFor(() => expect(canvas.getByText("Completed call: Confirm Friday delivery")).toBeInTheDocument());
  },
};

export const PlannedActivitiesSchedule: Story = {
  name: "planned activities: schedule one",
  beforeEach: fakeBackend(SEED, { planned: PLANNED_SEED }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const section = await waitFor(() => canvas.getByRole("region", { name: "Planned activities" }));
    await userEvent.click(within(section).getByRole("button", { name: "Schedule activity" }));
    await userEvent.type(within(section).getByRole("textbox", { name: /Summary/ }), "Book the delivery van");
    await userEvent.click(within(section).getByRole("button", { name: "Schedule" }));
    await waitFor(() => expect(within(section).getByText("Book the delivery van")).toBeInTheDocument());
  },
};

export const PlannedActivitiesEmpty: Story = {
  name: "planned activities: nothing planned, form open",
  beforeEach: fakeBackend(SEED),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getByText("Nothing planned.")).toBeInTheDocument());
    await userEvent.click(canvas.getByRole("button", { name: "Schedule activity" }));
  },
};
