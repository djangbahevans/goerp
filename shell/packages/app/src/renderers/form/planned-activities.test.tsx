import { AuthContext, type AuthContextValue, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { ScheduledActivity, UseRecordReadersResult, UseScheduledActivitiesResult } from "@goerp/sdk/react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { addDays, todayIn } from "../../activities/activity-dates.js";
import { PlannedActivities } from "./planned-activities.js";

const { useScheduledActivitiesMock, useRecordReadersMock, confirmMock } = vi.hoisted(() => ({
  useScheduledActivitiesMock: vi.fn(),
  useRecordReadersMock: vi.fn(),
  confirmMock: vi.fn(async () => true),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return {
    ...actual,
    useScheduledActivities: useScheduledActivitiesMock,
    useRecordReaders: useRecordReadersMock,
    useConfirm: () => ({ confirm: confirmMock }),
  };
});

const today = todayIn("UTC");
const ama = { id: "u1", name: "Ama Owusu", avatarUrl: null };
const kwame = { id: "u2", name: "Kwame Mensah", avatarUrl: null };
const esi = { id: "u3", name: "Esi Asante", avatarUrl: null };

function activity(overrides: Partial<ScheduledActivity> = {}): ScheduledActivity {
  return {
    id: "a1",
    model: "sales.order",
    recordId: "o1",
    type: "call",
    summary: "Confirm Friday delivery",
    note: null,
    dueDate: today,
    assignee: ama,
    createdBy: kwame,
    createdAt: "2026-09-23T16:02:00Z",
    doneAt: null,
    doneBy: null,
    feedback: null,
    ...overrides,
  };
}

function handle(overrides: Partial<UseScheduledActivitiesResult> = {}): UseScheduledActivitiesResult {
  return {
    activities: [],
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    schedule: vi.fn(async () => activity()),
    isScheduling: false,
    update: vi.fn(async () => activity()),
    markDone: vi.fn(async () => activity()),
    cancel: vi.fn(async () => {}),
    pendingIds: [],
    ...overrides,
  };
}

const readers: UseRecordReadersResult = { readers: [], isLoading: false, isError: false, error: null };
const auth = {
  user: { id: "u1", name: "Ama Owusu", email: "ama@acme.example", avatarUrl: null, timezone: "UTC" },
  tenant: { defaultTimezone: "UTC" },
} as unknown as AuthContextValue;

const permissions = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(),
});

function renderSection(h: UseScheduledActivitiesResult = handle()) {
  useScheduledActivitiesMock.mockReturnValue(h);
  const announce = vi.fn();
  render(
    <PermissionContext.Provider value={permissions}>
      <AuthContext.Provider value={auth}>
        <PlannedActivities model="sales.order" recordId="o1" announce={announce} />
      </AuthContext.Provider>
    </PermissionContext.Provider>,
  );
  return { h, announce };
}

const appError = (code: string) => new AppError({ code, message: `server: ${code}`, httpStatus: 400 });

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  useRecordReadersMock.mockReturnValue(readers);
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  confirmMock.mockResolvedValue(true);
});

describe("PlannedActivities", () => {
  it("shows a skeleton while loading, with scheduling already available", () => {
    renderSection(handle({ isLoading: true }));
    expect(document.querySelector("[data-skeleton='lines']")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Schedule activity" })).toBeTruthy();
  });

  it("shows a retryable load failure", () => {
    const { h } = renderSection(handle({ isError: true, error: appError("internal_error") }));
    expect(screen.getByRole("alert").textContent).toContain("Couldn't load planned activities.");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(h.refetch).toHaveBeenCalled();
  });

  it("renders nothing for a model without activities", () => {
    renderSection(handle({ isError: true, error: appError("activity_unsupported") }));
    expect(screen.queryByRole("region")).toBeNull();
    expect(screen.queryByText("Planned activities")).toBeNull();
  });

  it("says when nothing is planned", () => {
    renderSection();
    expect(screen.getByRole("region", { name: "Planned activities" })).toBeTruthy();
    expect(screen.getByText("Nothing planned.")).toBeTruthy();
  });

  it("shows each activity's type, summary, note, assignee and due date, marking overdue in text", () => {
    renderSection(
      handle({
        activities: [
          activity({ id: "a1", dueDate: addDays(today, -2), note: "Morning slot." }),
          activity({ id: "a2", dueDate: today, summary: "Send quote", type: "email", assignee: esi }),
        ],
      }),
    );
    const items = screen.getAllByRole("listitem");
    expect(items[0]?.textContent).toContain("Call, Confirm Friday delivery");
    expect(items[0]?.textContent).toContain("Morning slot.");
    expect(items[0]?.textContent).toContain("Ama Owusu (you)");
    expect(items[0]?.textContent).toMatch(/Overdue · /);
    expect(items[1]?.textContent).toContain("Email, Send quote");
    expect(items[1]?.textContent).toContain("Esi Asante");
    expect(items[1]?.textContent).toContain("Today");
  });

  it("offers actions only to the creator and the assignee", () => {
    renderSection(
      handle({
        activities: [
          activity({ id: "mine", summary: "Assigned to me" }),
          activity({ id: "made", summary: "Made by me", assignee: esi, createdBy: ama }),
          activity({ id: "other", summary: "Someone else's", assignee: esi, createdBy: kwame }),
        ],
      }),
    );
    expect(screen.getByRole("button", { name: 'Mark "Assigned to me" done' })).toBeTruthy();
    expect(screen.getByRole("button", { name: 'Edit "Made by me"' })).toBeTruthy();
    expect(screen.queryByRole("button", { name: `Mark "Someone else's" done` })).toBeNull();
    expect(screen.queryByRole("button", { name: `Cancel "Someone else's"` })).toBeNull();
  });

  it("marks done with trimmed feedback, announces it and moves focus to the next row", async () => {
    const first = activity({ id: "a1" });
    const second = activity({ id: "a2", summary: "Send quote" });
    const { h, announce } = renderSection(handle({ activities: [first, second] }));
    fireEvent.click(screen.getByRole("button", { name: 'Mark "Confirm Friday delivery" done' }));
    const box = screen.getByRole("textbox", { name: 'Feedback for "Confirm Friday delivery"' });
    expect(document.activeElement).toBe(box);
    fireEvent.change(box, { target: { value: "  Confirmed.  " } });
    fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect(announce).toHaveBeenCalledWith("Activity marked done"));
    expect(h.markDone).toHaveBeenCalledWith("a1", { feedback: "Confirmed." });
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Send quote"));
  });

  it("closes the feedback box on Escape and returns focus to Mark done", () => {
    renderSection(handle({ activities: [activity()] }));
    const markDone = screen.getByRole("button", { name: 'Mark "Confirm Friday delivery" done' });
    fireEvent.click(markDone);
    fireEvent.keyDown(screen.getByRole("textbox", { name: /Feedback for/ }), { key: "Escape" });
    expect(screen.queryByRole("textbox", { name: /Feedback for/ })).toBeNull();
    expect(document.activeElement?.getAttribute("aria-label")).toBe('Mark "Confirm Friday delivery" done');
  });

  it("moves focus to the heading when the last activity is marked done", async () => {
    renderSection(handle({ activities: [activity()] }));
    fireEvent.click(screen.getByRole("button", { name: /Mark .* done/ }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(document.activeElement?.textContent).toBe("Planned activities"));
  });

  it("shows a section message and refetches when the activity was already done", async () => {
    const { h } = renderSection(
      handle({ activities: [activity()], markDone: vi.fn(async () => Promise.reject(appError("activity_done"))) }),
    );
    fireEvent.click(screen.getByRole("button", { name: /Mark .* done/ }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(await screen.findByText("This activity was already marked done.")).toBeTruthy();
    expect(h.refetch).toHaveBeenCalled();
  });

  it("closes the feedback box and keeps focus on the row when the viewer can no longer change it", async () => {
    renderSection(
      handle({ activities: [activity()], markDone: vi.fn(async () => Promise.reject(appError("not_participant"))) }),
    );
    fireEvent.click(screen.getByRole("button", { name: /Mark .* done/ }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(await screen.findByText("You can no longer change this activity.")).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: /Feedback for/ })).toBeNull();
    await waitFor(() => expect(document.activeElement?.tagName).toBe("LI"));
  });

  it("moves focus on when the activity was done meanwhile", async () => {
    renderSection(
      handle({
        activities: [activity({ id: "a1" }), activity({ id: "a2", summary: "Send quote" })],
        markDone: vi.fn(async () => Promise.reject(appError("activity_done"))),
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: 'Mark "Confirm Friday delivery" done' }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Send quote"));
  });

  it("shows other mark-done failures at the row", async () => {
    renderSection(
      handle({ activities: [activity()], markDone: vi.fn(async () => Promise.reject(appError("internal_error"))) }),
    );
    fireEvent.click(screen.getByRole("button", { name: /Mark .* done/ }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(await screen.findByText("Couldn't mark this activity done.")).toBeTruthy();
  });

  it("cancels after confirmation, and not without it", async () => {
    const { h, announce } = renderSection(handle({ activities: [activity()] }));
    confirmMock.mockResolvedValueOnce(false);
    fireEvent.click(screen.getByRole("button", { name: 'Cancel "Confirm Friday delivery"' }));
    await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1));
    expect(h.cancel).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: 'Cancel "Confirm Friday delivery"' }));
    await waitFor(() => expect(h.cancel).toHaveBeenCalledWith("a1"));
    expect(confirmMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ title: "Cancel this activity?", confirmLabel: "Cancel activity", variant: "danger" }),
    );
    await waitFor(() => expect(announce).toHaveBeenCalledWith("Activity cancelled"));
  });

  it("schedules with the viewer as default assignee, today as default due date, and a trimmed summary", async () => {
    const { h, announce } = renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Schedule activity" }));
    expect((screen.getByRole("combobox", { name: "Assignee" }) as HTMLInputElement).value).toBe("Ama Owusu");
    fireEvent.change(screen.getByRole("textbox", { name: /Summary/ }), { target: { value: "  Call back  " } });
    fireEvent.click(screen.getByRole("button", { name: "Schedule" }));
    await waitFor(() =>
      expect(h.schedule).toHaveBeenCalledWith({ type: "call", summary: "Call back", dueDate: today, assigneeId: "u1" }),
    );
    expect(announce).toHaveBeenCalledWith("Activity scheduled");
    expect(screen.queryByRole("button", { name: "Schedule" })).toBeNull();
    await waitFor(() => expect(document.activeElement?.textContent).toBe("Schedule activity"));
  });

  it("requires a summary, focusing it", () => {
    const { h } = renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Schedule activity" }));
    fireEvent.click(screen.getByRole("button", { name: "Schedule" }));
    expect(screen.getByText("Enter a summary.")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("textbox", { name: /Summary/ }));
    expect(h.schedule).not.toHaveBeenCalled();
  });

  it("shows invalid_assignee on the Assignee field", async () => {
    renderSection(handle({ schedule: vi.fn(async () => Promise.reject(appError("invalid_assignee"))) }));
    fireEvent.click(screen.getByRole("button", { name: "Schedule activity" }));
    fireEvent.change(screen.getByRole("textbox", { name: /Summary/ }), { target: { value: "Call back" } });
    fireEvent.click(screen.getByRole("button", { name: "Schedule" }));
    expect(await screen.findByText("Ama Owusu can't see this record anymore. Choose someone else.")).toBeTruthy();
  });

  it("keeps the input and shows any other scheduling failure", async () => {
    renderSection(handle({ schedule: vi.fn(async () => Promise.reject(appError("internal_error"))) }));
    fireEvent.click(screen.getByRole("button", { name: "Schedule activity" }));
    fireEvent.change(screen.getByRole("textbox", { name: /Summary/ }), { target: { value: "Call back" } });
    fireEvent.click(screen.getByRole("button", { name: "Schedule" }));
    expect(await screen.findByText("server: internal_error")).toBeTruthy();
    expect((screen.getByRole("textbox", { name: /Summary/ }) as HTMLInputElement).value).toBe("Call back");
  });

  it("edits by sending only changed fields, sending a cleared note as null", async () => {
    const { h, announce } = renderSection(handle({ activities: [activity({ note: "Morning slot." })] }));
    fireEvent.click(screen.getByRole("button", { name: 'Edit "Confirm Friday delivery"' }));
    expect(document.activeElement).toBe(screen.getByRole("textbox", { name: /Summary/ }));
    fireEvent.change(screen.getByRole("textbox", { name: /Summary/ }), { target: { value: "Confirm Monday" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Note" }), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(h.update).toHaveBeenCalledWith("a1", { summary: "Confirm Monday", note: null }));
    expect(announce).toHaveBeenCalledWith("Activity updated");
    await waitFor(() => expect(document.activeElement?.tagName).toBe("LI"));
  });

  it("closes an unchanged edit without a request", async () => {
    const { h } = renderSection(handle({ activities: [activity()] }));
    fireEvent.click(screen.getByRole("button", { name: 'Edit "Confirm Friday delivery"' }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Save" })).toBeNull());
    expect(h.update).not.toHaveBeenCalled();
  });

  it("closes the edit form with a section message when the activity was done meanwhile", async () => {
    renderSection(
      handle({ activities: [activity()], update: vi.fn(async () => Promise.reject(appError("activity_done"))) }),
    );
    fireEvent.click(screen.getByRole("button", { name: 'Edit "Confirm Friday delivery"' }));
    fireEvent.change(screen.getByRole("textbox", { name: /Summary/ }), { target: { value: "Changed" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("This activity was already marked done.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  });
});
