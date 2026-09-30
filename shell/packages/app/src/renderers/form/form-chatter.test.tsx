import { AuthContext, type AuthContextValue, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type {
  ActivityEntry,
  RecordReader,
  UseRecordActivityResult,
  UseRecordFollowersResult,
  UseRecordReadersResult,
} from "@goerp/sdk/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FormChatter } from "./form-chatter.js";
import type { FormViewDeclaration } from "./form-view-types.js";

const { useRecordActivityMock, useRecordFollowersMock, useRecordReadersMock, confirmMock, resolveModelMock } =
  vi.hoisted(() => ({
    useRecordActivityMock: vi.fn(),
    useRecordFollowersMock: vi.fn(),
    useRecordReadersMock: vi.fn(),
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
    useRecordFollowers: useRecordFollowersMock,
    useRecordReaders: useRecordReadersMock,
    useConfirm: () => ({ confirm: confirmMock }),
    useRelationLabels: () => new Map([["contacts.contact|", { c2: "Acme Corp" }]]),
    useActivityTypes: () => ({
      types: [],
      isLoading: false,
      isError: false,
      getType: (key: string) =>
        key === "call"
          ? { key: "call", label: "Call", icon: "phone", defaultSummary: null, defaultDueDays: null, archived: false }
          : undefined,
    }),
    useScheduledActivities: () => ({
      activities: [],
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
      schedule: vi.fn(),
      isScheduling: false,
      update: vi.fn(),
      markDone: vi.fn(),
      cancel: vi.fn(),
      pendingIds: [],
    }),
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

function followersHandle(overrides: Partial<UseRecordFollowersResult> = {}): UseRecordFollowersResult {
  return {
    followers: [],
    isFollowing: false,
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    follow: vi.fn(async () => {}),
    unfollow: vi.fn(async () => {}),
    isUpdating: false,
    ...overrides,
  };
}

function readersHandle(overrides: Partial<UseRecordReadersResult> = {}): UseRecordReadersResult {
  return { readers: [], isLoading: false, isError: false, error: null, ...overrides };
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
  mentions: [],
  notifyFollowers: false,
  deleted: false,
  author: ama,
  createdAt: "2026-09-23T16:02:00Z",
  ...overrides,
});

beforeEach(() => {
  useRecordActivityMock.mockReturnValue(handle());
  useRecordFollowersMock.mockReturnValue(followersHandle());
  useRecordReadersMock.mockReturnValue(readersHandle());
});
afterEach(() => {
  cleanup();
  useRecordActivityMock.mockReset();
  useRecordFollowersMock.mockReset();
  useRecordReadersMock.mockReset();
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

  it("puts Planned activities above the composer", () => {
    renderChatter();
    const section = screen.getByRole("region", { name: "Planned activities" });
    const composer = screen.getByLabelText("Add a comment");
    expect(section.compareDocumentPosition(composer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("shows no Planned activities on an unsaved record", () => {
    renderChatter(null);
    expect(screen.queryByRole("region", { name: "Planned activities" })).toBeNull();
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
    expect(screen.getByText("Call completed: Confirm delivery")).toBeTruthy();
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

    await waitFor(() => expect(postComment).toHaveBeenCalledWith("Line one\nLine two", { notifyFollowers: false }));
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

const AMA_ID = "0196f3a2-0000-7000-8000-000000000011";
const AMANDA_ID = "0196f3a2-0000-7000-8000-000000000012";
const NAMELESS_ID = "0196f3a2-0000-7000-8000-000000000013";
const amaReader: RecordReader = { id: AMA_ID, name: "Ama", email: "ama@acme.example", avatarUrl: null };
const amandaReader: RecordReader = { id: AMANDA_ID, name: "Amanda", email: "amanda@acme.example", avatarUrl: null };
const namelessReader: RecordReader = { id: NAMELESS_ID, name: null, email: "kojo@acme.example", avatarUrl: null };

function follower(user: { id: string; name: string | null }) {
  return { user: { ...user, avatarUrl: null }, createdAt: "2026-09-23T16:02:00Z" };
}

describe("ChatterFollowBar", () => {
  it("shows the follower count and follows the record, announcing it", async () => {
    const follow = vi.fn(async () => {});
    useRecordFollowersMock.mockReturnValue(
      followersHandle({ followers: [follower(kwame), follower({ id: "u3", name: "Efua" })], follow }),
    );
    renderChatter();

    expect(screen.getByRole("button", { name: "Followers · 2" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Follow" }));

    await waitFor(() => expect(follow).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("You're following this record."));
  });

  it("offers Unfollow while following, and shows an inline error when it fails", async () => {
    const unfollow = vi.fn(async () => {
      throw new AppError({ code: "internal", message: "boom", httpStatus: 500 });
    });
    useRecordFollowersMock.mockReturnValue(
      followersHandle({ followers: [follower(ama)], isFollowing: true, unfollow }),
    );
    renderChatter();

    fireEvent.click(screen.getByRole("button", { name: "Unfollow" }));

    expect((await screen.findByRole("alert")).textContent).toBe("Couldn't update following. Try again.");
    expect(screen.getByRole("button", { name: "Unfollow" })).toBeTruthy();
  });

  it("guesses nothing while followers load", () => {
    useRecordFollowersMock.mockReturnValue(followersHandle({ isLoading: true }));
    renderChatter();

    expect(screen.getByRole("button", { name: "Followers" })).toBeTruthy();
    expect((screen.getByRole("button", { name: "Follow" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("checkbox", { name: "Notify followers" }) as HTMLInputElement).disabled).toBe(true);
  });

  it("hides the follow bar when followers fail to load, leaving the composer usable", () => {
    useRecordFollowersMock.mockReturnValue(
      followersHandle({ isError: true, error: new AppError({ code: "internal", message: "x", httpStatus: 500 }) }),
    );
    renderChatter();

    expect(screen.queryByRole("button", { name: /^Followers/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "Follow" })).toBeNull();
    expect((screen.getByRole("checkbox", { name: "Notify followers" }) as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByLabelText("Add a comment")).toBeTruthy();
  });

  it("lists followers in a popover, marks the viewer, and returns focus on Escape", async () => {
    useRecordFollowersMock.mockReturnValue(
      followersHandle({ followers: [follower(kwame), follower({ id: "u1", name: null })], isFollowing: true }),
    );
    renderChatter();
    const trigger = screen.getByRole("button", { name: "Followers · 2" });

    fireEvent.click(trigger);

    const dialog = await screen.findByRole("dialog", { name: "Followers" });
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    expect(dialog.textContent).toContain("Kwame Mensah");
    expect(dialog.textContent).toContain("Unknown user (you)");
    await waitFor(() => expect(document.activeElement?.textContent).toBe("Followers"));

    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(trigger);
  });

  it("says when no one follows the record", async () => {
    renderChatter();
    fireEvent.click(screen.getByRole("button", { name: "Followers · 0" }));
    expect((await screen.findByRole("dialog")).textContent).toContain("No one follows this record yet.");
  });
});

describe("Notify followers", () => {
  it("counts the other followers and sends a checked post as a message, then unchecks", async () => {
    const postComment = vi.fn(async () => comment());
    useRecordActivityMock.mockReturnValue(handle({ postComment }));
    useRecordFollowersMock.mockReturnValue(
      followersHandle({ followers: [follower(ama), follower(kwame)], isFollowing: true }),
    );
    renderChatter();
    const checkbox = screen.getByRole("checkbox", { name: "Notify followers (1)" }) as HTMLInputElement;

    fireEvent.click(checkbox);
    fireEvent.change(screen.getByLabelText("Add a comment"), { target: { value: "Moved to Friday." } });
    fireEvent.click(screen.getByRole("button", { name: "Comment" }));

    await waitFor(() => expect(postComment).toHaveBeenCalledWith("Moved to Friday.", { notifyFollowers: true }));
    await waitFor(() => expect(checkbox.checked).toBe(false));
  });

  it("posts a note once the checkbox is disabled, even if it was checked", async () => {
    const postComment = vi.fn(async () => comment());
    useRecordActivityMock.mockReturnValue(handle({ postComment }));
    useRecordFollowersMock.mockReturnValue(followersHandle({ followers: [follower(kwame)] }));
    const { rerender } = renderChatter();
    fireEvent.click(screen.getByRole("checkbox", { name: "Notify followers (1)" }));

    useRecordFollowersMock.mockReturnValue(followersHandle({ followers: [] }));
    rerender();
    const checkbox = screen.getByRole("checkbox", { name: "Notify followers (0)" }) as HTMLInputElement;
    expect(checkbox.disabled).toBe(true);
    expect(checkbox.checked).toBe(false);
    fireEvent.change(screen.getByLabelText("Add a comment"), { target: { value: "Hi" } });
    fireEvent.click(screen.getByRole("button", { name: "Comment" }));

    await waitFor(() => expect(postComment).toHaveBeenCalledWith("Hi", { notifyFollowers: false }));
  });

  it("is disabled when no one else follows the record", () => {
    useRecordFollowersMock.mockReturnValue(followersHandle({ followers: [follower(ama)], isFollowing: true }));
    renderChatter();
    expect((screen.getByRole("checkbox", { name: "Notify followers (0)" }) as HTMLInputElement).disabled).toBe(true);
  });
});

describe("Mention autocomplete", () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });

  function box(): HTMLTextAreaElement {
    return screen.getByLabelText("Add a comment") as HTMLTextAreaElement;
  }

  // Types value, with the caret left at its end, as a keystroke would.
  function typeText(value: string) {
    fireEvent.change(box(), { target: { value } });
  }

  it("opens on @, highlights the first candidate, and inserts the chosen name", async () => {
    useRecordReadersMock.mockReturnValue(readersHandle({ readers: [amaReader, amandaReader] }));
    renderChatter();

    typeText("Hi @");
    typeText("Hi @am");

    expect(useRecordReadersMock).toHaveBeenLastCalledWith("sales.order", "o1", "am", { excludeSelf: true });
    const listbox = screen.getByRole("listbox");
    const options = screen.getAllByRole("option");
    expect(options.map((o) => o.getAttribute("aria-label"))).toEqual([
      "Ama, ama@acme.example",
      "Amanda, amanda@acme.example",
    ]);
    expect(box().getAttribute("aria-controls")).toBe(listbox.id);
    expect(box().getAttribute("aria-activedescendant")).toBe(options[0]?.id);
    expect(box().getAttribute("role")).toBeNull();

    fireEvent.keyDown(box(), { key: "ArrowDown" });
    expect(box().getAttribute("aria-activedescendant")).toBe(options[1]?.id);
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    expect(box().getAttribute("aria-activedescendant")).toBe(options[0]?.id);

    fireEvent.keyDown(box(), { key: "Enter" });
    expect(box().value).toBe("Hi @Ama ");
    expect(screen.queryByRole("listbox")).toBeNull();
    await waitFor(() => expect(screen.getByRole("status").textContent?.trim()).toBe("2 people found"));
  });

  it("chooses on Tab, and encodes only the chosen name when posting", async () => {
    const postComment = vi.fn(async () => comment());
    useRecordActivityMock.mockReturnValue(handle({ postComment }));
    useRecordReadersMock.mockReturnValue(readersHandle({ readers: [amaReader] }));
    renderChatter();

    typeText("@");
    typeText("@Am");
    fireEvent.keyDown(box(), { key: "Tab" });
    expect(box().value).toBe("@Ama ");
    typeText("@Ama please ask @Amanda and @Ama Owusu.");
    fireEvent.keyDown(box(), { key: "Enter", ctrlKey: true });

    await waitFor(() =>
      expect(postComment).toHaveBeenCalledWith(`<@${AMA_ID}> please ask @Amanda and <@${AMA_ID}> Owusu.`, {
        notifyFollowers: false,
      }),
    );
  });

  it("inserts a nameless candidate as their email's local part", () => {
    useRecordReadersMock.mockReturnValue(readersHandle({ readers: [namelessReader] }));
    renderChatter();

    typeText("@");
    typeText("@ko");
    fireEvent.keyDown(box(), { key: "Enter" });

    expect(box().value).toBe("@kojo ");
  });

  it("leaves Enter and Tab alone while nothing is highlighted", () => {
    useRecordReadersMock.mockReturnValue(readersHandle({ readers: [amaReader], isLoading: true }));
    renderChatter();

    typeText("@");
    typeText("@am");

    expect(box().getAttribute("aria-activedescendant")).toBeNull();
    expect(fireEvent.keyDown(box(), { key: "Enter" })).toBe(true);
    expect(fireEvent.keyDown(box(), { key: "Tab" })).toBe(true);
  });

  it("shows why no one is listed, and the load error", async () => {
    renderChatter();
    typeText("@");
    typeText("@zz");
    expect(screen.getByRole("listbox").textContent).toBe("No one found who can see this record.");
    await waitFor(() =>
      expect(screen.getByRole("status").textContent?.trim()).toBe("No one found who can see this record."),
    );

    useRecordReadersMock.mockReturnValue(readersHandle({ isError: true }));
    typeText("@zzz");
    expect(screen.getByRole("listbox").textContent).toBe("Couldn't load people.");
  });

  it("closes on Escape, keeps the query as text, and doesn't reopen until a new @", () => {
    useRecordReadersMock.mockReturnValue(readersHandle({ readers: [amaReader] }));
    renderChatter();

    typeText("@");
    typeText("@am");
    fireEvent.keyDown(box(), { key: "Escape" });

    expect(screen.queryByRole("listbox")).toBeNull();
    expect(box().value).toBe("@am");
    typeText("@ama");
    expect(screen.queryByRole("listbox")).toBeNull();
    typeText("@ama @");
    expect(screen.getByRole("listbox")).toBeTruthy();
  });

  it("doesn't start a query for an @ inside a word", () => {
    renderChatter();
    typeText("mail ama@");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("names the recorded users an invalid_mention post was rejected for", async () => {
    const postComment = vi.fn(async () => {
      throw new AppError({
        code: "invalid_mention",
        message: "a mentioned user can't read this record",
        httpStatus: 400,
        details: { user_ids: [AMA_ID, AMANDA_ID] },
      });
    });
    useRecordActivityMock.mockReturnValue(handle({ postComment }));
    renderChatter();

    for (const reader of [amaReader, amandaReader]) {
      useRecordReadersMock.mockReturnValue(readersHandle({ readers: [reader] }));
      typeText(`${box().value}@`);
      fireEvent.keyDown(box(), { key: "Enter" });
    }
    expect(box().value).toBe("@Ama @Amanda ");
    fireEvent.click(screen.getByRole("button", { name: "Comment" }));

    expect((await screen.findByRole("alert")).textContent).toBe(
      "Ama and Amanda can't see this record, so they can't be mentioned. Remove the mention and post again.",
    );
    expect(box().value).toBe("@Ama @Amanda ");
  });
});

describe("Comment entries", () => {
  it("renders mentions by name, highlights the viewer's, and titles a message", () => {
    useRecordActivityMock.mockReturnValue(
      handle({
        entries: [
          comment({
            id: "m1",
            body: `<@${AMA_ID}> and <@0196f3a2-0000-7000-8000-0000000000aa>, please check.`,
            mentions: [
              { id: AMA_ID, name: "Ama", email: "ama@acme.example" },
              { id: "0196f3a2-0000-7000-8000-0000000000aa", name: null, email: null },
            ],
            notifyFollowers: true,
            author: kwame,
          }),
        ],
      }),
    );
    renderChatter();

    expect(screen.getByText("Messaged followers")).toBeTruthy();
    const body = screen.getByText(/please check/);
    expect(body.textContent).toBe("@Ama and @Unknown user, please check.");
  });

  it("highlights a mention of the viewer", () => {
    useRecordActivityMock.mockReturnValue(
      handle({
        entries: [
          comment({
            body: "<@0196f3a2-0000-7000-8000-0000000000b1> see this",
            mentions: [{ id: "0196f3a2-0000-7000-8000-0000000000b1", name: "Ama Owusu", email: "a@x.example" }],
            author: kwame,
          }),
        ],
      }),
    );
    auth.user = { id: "0196f3a2-0000-7000-8000-0000000000b1" } as never;
    try {
      renderChatter();
      expect(screen.getByText("@Ama Owusu").className).toContain("bg-primary-subtle");
    } finally {
      auth.user = { id: "u1" } as never;
    }
  });
});
