import { AuthContext, type AuthContextValue, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { ActivityEntry, UseRecordActivityResult } from "@goerp/sdk/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FormChatter } from "./form-chatter.js";
import type { FormViewDeclaration } from "./form-view-types.js";

const { useRecordActivityMock, confirmMock, resolveModelMock } = vi.hoisted(() => ({
  useRecordActivityMock: vi.fn(),
  confirmMock: vi.fn(async () => true),
  resolveModelMock: vi.fn(async () => ({
    name: "sales.order",
    fields: [
      { name: "state", type: "selection" },
      { name: "customer_id", type: "many2one", related_model: "contacts.contact" },
    ],
  })),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return {
    ...actual,
    useRecordActivity: useRecordActivityMock,
    useConfirm: () => ({ confirm: confirmMock }),
    useRelationLabels: () => new Map([["contacts.contact|", { c2: "Acme Corp" }]]),
  };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, modelRegistry: { resolve: resolveModelMock } };
});

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
      ],
    },
  ],
};

const ama = { id: "u1", name: "Ama Owusu", avatarUrl: null };
const kwame = { id: "u2", name: "Kwame Mensah", avatarUrl: null };

function handle(overrides: Partial<UseRecordActivityResult> = {}): UseRecordActivityResult {
  return {
    entries: [],
    isLoading: false,
    isError: false,
    error: null,
    hasMore: false,
    fetchMore: vi.fn(),
    isFetchingNextPage: false,
    refetch: vi.fn(),
    postComment: vi.fn(async () => ({}) as ActivityEntry),
    isPosting: false,
    deleteComment: vi.fn(async () => {}),
    deletingIds: [],
    ...overrides,
  };
}

const permissionValue = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(),
});
const auth = { user: { id: "u1" } } as unknown as AuthContextValue;

function renderChatter(recordId: string | null = "o1") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = () => (
    <QueryClientProvider client={client}>
      <PermissionContext.Provider value={permissionValue}>
        <AuthContext.Provider value={auth}>
          <FormChatter view={view} recordId={recordId ?? undefined} />
        </AuthContext.Provider>
      </PermissionContext.Provider>
    </QueryClientProvider>
  );
  const result = render(tree());
  return { ...result, rerender: () => result.rerender(tree()) };
}

const comment = (overrides: Partial<Extract<ActivityEntry, { kind: "comment" }>> = {}): ActivityEntry => ({
  id: "c1",
  kind: "comment",
  body: "Customer asked to move delivery to Friday.",
  deleted: false,
  author: ama,
  createdAt: "2026-09-23T16:02:00Z",
  ...overrides,
});

beforeEach(() => useRecordActivityMock.mockReturnValue(handle()));
afterEach(() => {
  cleanup();
  useRecordActivityMock.mockReset();
  confirmMock.mockReset();
  confirmMock.mockResolvedValue(true);
});

describe("FormChatter", () => {
  it("shows a save-first message and no composer for an unsaved record", () => {
    renderChatter(null);
    expect(screen.getByText("Activity appears here once this record is saved.")).toBeTruthy();
    expect(screen.queryByLabelText("Add a comment")).toBeNull();
    expect(useRecordActivityMock).not.toHaveBeenCalled();
  });

  it("renders nothing for a model with no activity feed", () => {
    useRecordActivityMock.mockReturnValue(
      handle({
        isError: true,
        error: new AppError({ code: "activity_unsupported", message: "unsupported", httpStatus: 400 }),
      }),
    );
    const { container } = renderChatter();
    expect(container.textContent).toBe("");
  });

  it("shows a loading skeleton with the composer already usable", () => {
    useRecordActivityMock.mockReturnValue(handle({ isLoading: true }));
    const { container } = renderChatter();
    expect(container.querySelector('[data-skeleton="lines"]')).toBeTruthy();
    expect(screen.getByLabelText("Add a comment")).toBeTruthy();
  });

  it("shows a load error with a Retry that refetches", () => {
    const refetch = vi.fn();
    useRecordActivityMock.mockReturnValue(
      handle({ isError: true, error: new AppError({ code: "internal", message: "boom", httpStatus: 500 }), refetch }),
    );
    renderChatter();
    expect(screen.getByRole("alert").textContent).toContain("Couldn't load activity.");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(refetch).toHaveBeenCalled();
  });

  it("shows an empty state when the feed has no entries", () => {
    renderChatter();
    expect(screen.getByText("No activity yet")).toBeTruthy();
  });

  it("renders each entry kind with its title and body", async () => {
    useRecordActivityMock.mockReturnValue(
      handle({
        entries: [
          {
            id: "e1",
            kind: "change",
            changes: [{ field: "state", old: "draft", new: "confirmed" }],
            author: ama,
            createdAt: "2026-09-24T10:15:00Z",
          },
          {
            id: "e2",
            kind: "change",
            changes: [
              { field: "state", old: "confirmed", new: "draft" },
              { field: "customer_id", old: null, new: "c2" },
            ],
            author: null,
            createdAt: "2026-09-24T09:00:00Z",
          },
          comment({ id: "e3", author: kwame }),
          comment({ id: "e4", body: null, deleted: true }),
          {
            id: "e5",
            kind: "activity_done",
            activity: {
              activityId: "a1",
              type: "call",
              summary: "Confirm delivery",
              dueDate: "2026-09-20",
              feedback: "Done",
            },
            author: ama,
            createdAt: "2026-09-21T08:00:00Z",
          },
          { id: "e6", kind: "created", author: ama, createdAt: "2026-09-19T08:00:00Z" },
        ],
      }),
    );
    renderChatter();

    expect(screen.getByText("Changed Status")).toBeTruthy();
    expect(screen.getByText("Changed 2 fields by the system")).toBeTruthy();
    // Values resolve once the model schema loads.
    expect(
      await screen.findByText((_, el) => el?.tagName === "LI" && el.textContent === "Draft → Confirmed"),
    ).toBeTruthy();
    expect(
      screen.getByText((_, el) => el?.tagName === "LI" && el.textContent === "Customer: — → Acme Corp"),
    ).toBeTruthy();
    expect(screen.getByText("Customer asked to move delivery to Friday.")).toBeTruthy();
    expect(screen.getByText("Comment deleted")).toBeTruthy();
    expect(screen.getByText("Completed call: Confirm delivery")).toBeTruthy();
    expect(screen.getByText("Done")).toBeTruthy();
    expect(screen.getByText("Created this record")).toBeTruthy();
  });

  it("offers delete only on the viewer's own, undeleted comments", () => {
    useRecordActivityMock.mockReturnValue(
      handle({
        entries: [
          comment({ id: "mine" }),
          comment({ id: "theirs", author: kwame }),
          comment({ id: "gone", deleted: true, body: null }),
        ],
      }),
    );
    renderChatter();
    expect(screen.getAllByRole("button", { name: /^Delete comment from / })).toHaveLength(1);
  });

  it("deletes a comment after confirmation and focuses its entry", async () => {
    const deleteComment = vi.fn(async () => {});
    useRecordActivityMock.mockReturnValue(handle({ entries: [comment()], deleteComment }));
    const { rerender } = renderChatter();

    fireEvent.click(screen.getByRole("button", { name: /^Delete comment from / }));

    await waitFor(() => expect(deleteComment).toHaveBeenCalledWith("c1"));
    expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ variant: "danger", confirmLabel: "Delete" }));

    useRecordActivityMock.mockReturnValue(handle({ entries: [comment({ deleted: true, body: null })], deleteComment }));
    rerender();
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Comment deleted"));
  });

  it("does not delete when the confirmation is cancelled", async () => {
    confirmMock.mockResolvedValue(false);
    const deleteComment = vi.fn(async () => {});
    useRecordActivityMock.mockReturnValue(handle({ entries: [comment()], deleteComment }));
    renderChatter();

    fireEvent.click(screen.getByRole("button", { name: /^Delete comment from / }));

    await waitFor(() => expect(confirmMock).toHaveBeenCalled());
    expect(deleteComment).not.toHaveBeenCalled();
  });

  it("shows an inline error under a comment whose delete failed", async () => {
    const deleteComment = vi.fn(async () => {
      throw new Error("nope");
    });
    useRecordActivityMock.mockReturnValue(handle({ entries: [comment()], deleteComment }));
    renderChatter();

    fireEvent.click(screen.getByRole("button", { name: /^Delete comment from / }));

    expect((await screen.findByRole("alert")).textContent).toBe("Couldn't delete this comment.");
  });

  it("announces loaded entries and focuses the first of the last page", async () => {
    const fetchMore = vi.fn();
    const older = comment({ id: "c0", body: "Older", author: kwame });
    useRecordActivityMock.mockReturnValue(handle({ entries: [comment()], hasMore: true, fetchMore }));
    const { rerender } = renderChatter();

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(fetchMore).toHaveBeenCalled();
    useRecordActivityMock.mockReturnValue(
      handle({ entries: [comment()], hasMore: true, fetchMore, isFetchingNextPage: true }),
    );
    rerender();
    useRecordActivityMock.mockReturnValue(handle({ entries: [comment(), older], hasMore: false, fetchMore }));
    rerender();

    expect(screen.getByRole("status").textContent).toBe("1 more entry loaded");
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Older"));
  });

  it("reports a failed next page, and doesn't mistake a later refetch for it", () => {
    const fetchMore = vi.fn();
    const failed = { isError: true, error: new AppError({ code: "internal", message: "boom", httpStatus: 500 }) };
    useRecordActivityMock.mockReturnValue(handle({ entries: [comment()], hasMore: true, fetchMore }));
    const { rerender } = renderChatter();

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    useRecordActivityMock.mockReturnValue(
      handle({ entries: [comment()], hasMore: true, fetchMore, isFetchingNextPage: true }),
    );
    rerender();
    useRecordActivityMock.mockReturnValue(handle({ entries: [comment()], hasMore: true, fetchMore, ...failed }));
    rerender();
    expect(screen.getByRole("alert").textContent).toBe("Couldn't load more activity.");

    // A posted comment's refetch adds an entry; it isn't a "Load more" result.
    useRecordActivityMock.mockReturnValue(
      handle({ entries: [comment({ id: "c9", body: "New" }), comment()], hasMore: true, fetchMore }),
    );
    rerender();
    expect(screen.getByRole("status").textContent).toBe("");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("renders no body wrapper for an entry without a body", () => {
    useRecordActivityMock.mockReturnValue(
      handle({ entries: [{ id: "e1", kind: "created", author: ama, createdAt: "2026-09-19T08:00:00Z" }] }),
    );
    const { container } = renderChatter();
    expect(container.querySelector("li .mt-1")).toBeNull();
  });
});

describe("ChatterComposer", () => {
  it("disables Comment until the box has non-whitespace text", () => {
    renderChatter();
    const button = screen.getByRole("button", { name: "Comment" }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Add a comment"), { target: { value: "   " } });
    expect(button.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Add a comment"), { target: { value: "Hi" } });
    expect(button.disabled).toBe(false);
  });

  it("posts on Ctrl+Enter, clears the box and announces the post", async () => {
    const postComment = vi.fn(async () => comment());
    useRecordActivityMock.mockReturnValue(handle({ postComment }));
    renderChatter();
    const box = screen.getByLabelText("Add a comment") as HTMLTextAreaElement;

    fireEvent.change(box, { target: { value: "Line one\nLine two" } });
    fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });

    await waitFor(() => expect(postComment).toHaveBeenCalledWith("Line one\nLine two"));
    await waitFor(() => expect(box.value).toBe(""));
    expect(screen.getByRole("status").textContent).toBe("Comment posted");
  });

  it("does not post on a plain Enter", () => {
    const postComment = vi.fn(async () => comment());
    useRecordActivityMock.mockReturnValue(handle({ postComment }));
    renderChatter();
    const box = screen.getByLabelText("Add a comment");

    fireEvent.change(box, { target: { value: "Hi" } });
    fireEvent.keyDown(box, { key: "Enter" });

    expect(postComment).not.toHaveBeenCalled();
  });

  it("keeps the text and shows the error when a post fails", async () => {
    const postComment = vi.fn(async () => {
      throw new AppError({ code: "invalid_request", message: "Comment is too long.", httpStatus: 400 });
    });
    useRecordActivityMock.mockReturnValue(handle({ postComment }));
    renderChatter();
    const box = screen.getByLabelText("Add a comment") as HTMLTextAreaElement;

    fireEvent.change(box, { target: { value: "Hi" } });
    fireEvent.click(screen.getByRole("button", { name: "Comment" }));

    expect((await screen.findByRole("alert")).textContent).toBe("Comment is too long.");
    expect(box.value).toBe("Hi");
    expect(box.getAttribute("aria-invalid")).toBe("true");
  });

  it("makes the box read-only while a post is in flight", () => {
    useRecordActivityMock.mockReturnValue(handle({ isPosting: true }));
    renderChatter();
    expect((screen.getByLabelText("Add a comment") as HTMLTextAreaElement).readOnly).toBe(true);
  });
});
