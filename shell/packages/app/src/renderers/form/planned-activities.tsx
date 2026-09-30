import { useAuth } from "@goerp/sdk/auth";
import {
  ActionButton,
  Button,
  DateField,
  FieldWrapper,
  Icon,
  IconButton,
  Select,
  Skeleton,
  TextArea,
  TextInput,
  UserAvatar,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import {
  type ActivityType,
  type RecordReader,
  type ScheduledActivity,
  type UpdateScheduledActivityInput,
  useActivityTypes,
  useConfirm,
  useScheduledActivities,
} from "@goerp/sdk/react";
import { type KeyboardEvent, type ReactNode, type SubmitEvent, useEffect, useId, useRef, useState } from "react";
import { dueLabel, todayIn } from "../../activities/activity-dates.js";
import { useActivityTimezone } from "../../activities/use-due-activity-count.js";
import { RecordReaderPicker } from "./record-reader-picker.js";

// docs/components/planned-activities.md.
export interface PlannedActivitiesProps {
  model: string;
  recordId: string;
  announce: (message: string) => void;
}

// Outcomes that take the activity out of the list or out of the user's reach.
const GONE_MESSAGES: Record<string, string> = {
  activity_done: "This activity was already marked done.",
  not_participant: "You can no longer change this activity.",
  permission_denied: "You can no longer change this activity.",
  not_found: "This activity no longer exists.",
};

function goneMessage(error: unknown): string | undefined {
  return error instanceof AppError ? GONE_MESSAGES[error.code] : undefined;
}

type FocusTarget = { kind: "row"; id: string } | { kind: "heading" } | { kind: "schedule" };

export function PlannedActivities({ model, recordId, announce }: PlannedActivitiesProps): ReactNode {
  const activities = useScheduledActivities(model, recordId);
  const activityTypes = useActivityTypes();
  const { confirm } = useConfirm();
  const viewer = useAuth().user;
  const today = todayIn(useActivityTimezone());
  const headingId = useId();
  const headingRef = useRef<HTMLHeadingElement | null>(null);
  const scheduleButtonRef = useRef<HTMLButtonElement | null>(null);
  const rowRefs = useRef(new Map<string, HTMLLIElement>());
  const [scheduling, setScheduling] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [focusTarget, setFocusTarget] = useState<FocusTarget | null>(null);

  // biome-ignore lint/correctness/useExhaustiveDependencies: `activities.activities` re-runs the lookup once a refetched row has rendered.
  useEffect(() => {
    if (focusTarget === null) return;
    const element =
      focusTarget.kind === "row"
        ? rowRefs.current.get(focusTarget.id)
        : focusTarget.kind === "heading"
          ? headingRef.current
          : scheduleButtonRef.current;
    if (element) {
      element.focus();
      setFocusTarget(null);
    }
  }, [focusTarget, activities.activities]);

  if (activities.isError && activities.error?.code === "activity_unsupported") return null;

  const list = activities.activities;

  // Where focus goes once the activity at `index` leaves the list.
  function focusAfterRemoving(index: number): FocusTarget {
    const next = list[index + 1] ?? list[index - 1];
    return next ? { kind: "row", id: next.id } : { kind: "heading" };
  }

  // A done or deleted activity leaves the list on refetch, so focus moves on
  // as it would after a success; one the viewer can no longer change stays.
  function handleGone(error: unknown, activity: ScheduledActivity, index: number): boolean {
    const text = goneMessage(error);
    if (text === undefined) return false;
    setMessage(text);
    const code = (error as AppError).code;
    setFocusTarget(
      code === "activity_done" || code === "not_found" ? focusAfterRemoving(index) : { kind: "row", id: activity.id },
    );
    activities.refetch();
    return true;
  }

  async function markDone(activity: ScheduledActivity, index: number, feedback: string): Promise<void> {
    setMessage(null);
    const trimmed = feedback.trim();
    try {
      await activities.markDone(activity.id, trimmed ? { feedback: trimmed } : undefined);
    } catch (error) {
      if (handleGone(error, activity, index)) return;
      throw error;
    }
    setFocusTarget(focusAfterRemoving(index));
    announce("Activity marked done");
  }

  async function cancel(activity: ScheduledActivity, index: number): Promise<void> {
    const confirmed = await confirm({
      title: "Cancel this activity?",
      description: "It's removed without an entry in the activity feed. This can't be undone.",
      confirmLabel: "Cancel activity",
      cancelLabel: "Keep",
      variant: "danger",
    });
    if (!confirmed) return;
    setMessage(null);
    try {
      await activities.cancel(activity.id);
    } catch (error) {
      if (handleGone(error, activity, index)) return;
      throw error;
    }
    setFocusTarget(focusAfterRemoving(index));
    announce("Activity cancelled");
  }

  function renderList(): ReactNode {
    if (activities.isLoading) return <Skeleton lines={2} />;
    if (activities.isError) {
      return (
        <div role="alert" className="flex flex-col items-center gap-2 py-2 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-sm text-text">Couldn't load planned activities.</p>
          <ActionButton variant="secondary" size="sm" onClick={activities.refetch}>
            Retry
          </ActionButton>
        </div>
      );
    }
    if (list.length === 0) return <p className="text-sm text-text-secondary">Nothing planned.</p>;
    return (
      <ul className="flex flex-col divide-y divide-border">
        {list.map((activity, index) => (
          <ActivityRow
            key={activity.id}
            ref={(item) => {
              if (item) rowRefs.current.set(activity.id, item);
              else rowRefs.current.delete(activity.id);
            }}
            model={model}
            recordId={recordId}
            activity={activity}
            activityTypes={activityTypes}
            today={today}
            viewerId={viewer?.id}
            viewerEmail={viewer?.email}
            pending={activities.pendingIds.includes(activity.id)}
            onMarkDone={(feedback) => markDone(activity, index, feedback)}
            onCancel={() => cancel(activity, index)}
            onUpdate={async (changes) => {
              setMessage(null);
              await activities.update(activity.id, changes);
              announce("Activity updated");
            }}
            onSaved={() => setFocusTarget({ kind: "row", id: activity.id })}
            onGone={(error) => handleGone(error, activity, index)}
          />
        ))}
      </ul>
    );
  }

  const viewerAsReader: RecordReader | null = viewer
    ? { id: viewer.id, name: viewer.name, email: viewer.email, avatarUrl: viewer.avatarUrl }
    : null;
  const firstActiveType = activityTypes.types.find((t) => !t.archived);

  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-2">
      <div className="flex min-h-8 items-center justify-between gap-2">
        <h3
          id={headingId}
          ref={headingRef}
          tabIndex={-1}
          className="font-semibold text-sm text-text focus:outline-none"
        >
          Planned activities
        </h3>
        {!scheduling && (
          <Button
            ref={scheduleButtonRef}
            variant="secondary"
            size="sm"
            icon="calendar-plus"
            onClick={() => {
              setMessage(null);
              setScheduling(true);
            }}
          >
            Schedule activity
          </Button>
        )}
      </div>
      {scheduling && (
        <ActivityForm
          model={model}
          recordId={recordId}
          activityTypes={activityTypes}
          today={today}
          initial={{
            type: firstActiveType?.key ?? "",
            dueDate: dueDateFromDefault(today, firstActiveType?.defaultDueDays),
            summary: firstActiveType?.defaultSummary ?? "",
            assignee: viewerAsReader,
            note: "",
          }}
          submitLabel="Schedule"
          autoFocusField="type"
          onSubmit={async (values) => {
            setMessage(null);
            await activities.schedule({
              type: values.type,
              summary: values.summary,
              dueDate: values.dueDate,
              ...(values.note ? { note: values.note } : {}),
              ...(values.assignee ? { assigneeId: values.assignee.id } : {}),
            });
            setScheduling(false);
            setFocusTarget({ kind: "schedule" });
            announce("Activity scheduled");
          }}
          onCancel={() => {
            setScheduling(false);
            setFocusTarget({ kind: "schedule" });
          }}
        />
      )}
      {message && (
        <p role="alert" className="text-danger text-sm">
          {message}
        </p>
      )}
      {renderList()}
    </section>
  );
}

// The bits of useActivityTypes() this file threads through props.
interface ActivityTypesLookup {
  types: ActivityType[];
  getType: (key: string) => ActivityType | undefined;
}

// DateField reads and writes date-only values as UTC midnight; today and
// dueDays are both in the user's own timezone (scheduled-activities.md §9
// "Using a type").
function dueDateFromDefault(today: string, dueDays: number | null | undefined): string {
  if (dueDays === undefined || dueDays === null) return today;
  const date = new Date(`${today}T00:00:00Z`);
  date.setUTCDate(date.getUTCDate() + dueDays);
  return date.toISOString().slice(0, 10);
}

interface ActivityRowProps {
  ref: (item: HTMLLIElement | null) => void;
  model: string;
  recordId: string;
  activity: ScheduledActivity;
  activityTypes: ActivityTypesLookup;
  today: string;
  viewerId: string | undefined;
  viewerEmail: string | undefined;
  pending: boolean;
  onMarkDone: (feedback: string) => Promise<void>;
  onCancel: () => Promise<void>;
  onUpdate: (changes: UpdateScheduledActivityInput) => Promise<void>;
  onSaved: () => void;
  onGone: (error: unknown) => boolean;
}

function ActivityRow({
  ref,
  model,
  recordId,
  activity,
  activityTypes,
  today,
  viewerId,
  viewerEmail,
  pending,
  onMarkDone,
  onCancel,
  onUpdate,
  onSaved,
  onGone,
}: ActivityRowProps): ReactNode {
  const [mode, setMode] = useState<"view" | "done" | "edit">("view");
  const [feedback, setFeedback] = useState("");
  const [rowError, setRowError] = useState<string | null>(null);
  const markDoneRef = useRef<HTMLButtonElement | null>(null);
  const editRef = useRef<HTMLButtonElement | null>(null);
  const [returnFocus, setReturnFocus] = useState<"markDone" | "edit" | null>(null);

  useEffect(() => {
    if (mode !== "view" || returnFocus === null) return;
    (returnFocus === "markDone" ? markDoneRef : editRef).current?.focus();
    setReturnFocus(null);
  }, [mode, returnFocus]);

  const type = activityTypes.getType(activity.type) ?? { label: activity.type, icon: "calendar-check" };
  const participant =
    viewerId !== undefined && (viewerId === activity.createdBy.id || viewerId === activity.assignee.id);
  const overdue = activity.dueDate < today;
  const due = overdue ? `Overdue · ${dueLabel(activity.dueDate, today)}` : dueLabel(activity.dueDate, today);
  // An activity carries no emails, but the viewer knows their own.
  const assigneeName =
    activity.assignee.name ?? (activity.assignee.id === viewerId && viewerEmail ? viewerEmail : "Unknown user");

  async function submitDone(): Promise<void> {
    setRowError(null);
    try {
      await onMarkDone(feedback);
      // The row may stay listed after a refused completion, so the box closes either way.
      setMode("view");
    } catch {
      setRowError("Couldn't mark this activity done.");
    }
  }

  async function cancel(): Promise<void> {
    setRowError(null);
    try {
      await onCancel();
    } catch {
      setRowError("Couldn't cancel this activity.");
    }
  }

  function onFeedbackKeyDown(event: KeyboardEvent<HTMLInputElement>): void {
    if (event.key === "Enter") {
      event.preventDefault();
      if (!pending) void submitDone();
    } else if (event.key === "Escape" && !pending) {
      event.stopPropagation();
      setMode("view");
      setReturnFocus("markDone");
    }
  }

  if (mode === "edit") {
    return (
      <li ref={ref} tabIndex={-1} className="py-2 focus:outline-none">
        <ActivityForm
          model={model}
          recordId={recordId}
          activityTypes={activityTypes}
          today={today}
          initial={{
            type: activity.type,
            dueDate: activity.dueDate,
            summary: activity.summary,
            assignee: {
              id: activity.assignee.id,
              name: assigneeName,
              email: "",
              avatarUrl: activity.assignee.avatarUrl,
            },
            note: activity.note ?? "",
          }}
          submitLabel="Save"
          autoFocusField="summary"
          onSubmit={async (values) => {
            const changes: UpdateScheduledActivityInput = {};
            if (values.type !== activity.type) changes.type = values.type;
            if (values.summary !== activity.summary) changes.summary = values.summary;
            if (values.dueDate !== activity.dueDate) changes.dueDate = values.dueDate;
            if (values.assignee && values.assignee.id !== activity.assignee.id) changes.assigneeId = values.assignee.id;
            if (values.note !== (activity.note ?? "")) changes.note = values.note === "" ? null : values.note;
            if (Object.keys(changes).length > 0) await onUpdate(changes);
            setMode("view");
            onSaved();
          }}
          onError={(error) => {
            if (!onGone(error)) return false;
            setMode("view");
            return true;
          }}
          onCancel={() => {
            setMode("view");
            setReturnFocus("edit");
          }}
        />
      </li>
    );
  }

  return (
    <li ref={ref} tabIndex={-1} className="flex flex-col gap-2 py-2 focus:outline-none">
      <div className="flex flex-wrap items-start gap-3">
        <span className="mt-0.5 flex-none text-text-secondary">
          <Icon name={type.icon} size={16} aria-hidden="true" />
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="wrap-anywhere text-sm text-text">
            <span className="sr-only">{type.label}, </span>
            {activity.summary}
          </span>
          {activity.note && (
            <p className="wrap-anywhere max-w-[80ch] whitespace-pre-wrap text-sm text-text-secondary">
              {activity.note}
            </p>
          )}
          <span className="flex flex-wrap items-center gap-1 text-text-secondary text-xs">
            <UserAvatar
              userId={activity.assignee.id}
              name={assigneeName}
              avatarUrl={activity.assignee.avatarUrl}
              size="xs"
            />
            <span>
              {assigneeName}
              {activity.assignee.id === viewerId && " (you)"}
            </span>
            <span aria-hidden="true">·</span>
            <span className={overdue ? "text-danger" : undefined}>{due}</span>
          </span>
        </div>
        {participant && mode === "view" && (
          <div className="flex flex-none items-center gap-1">
            <Button
              ref={markDoneRef}
              variant="secondary"
              size="sm"
              disabled={pending}
              onClick={() => {
                setRowError(null);
                setMode("done");
              }}
              aria-label={`Mark "${activity.summary}" done`}
            >
              Mark done
            </Button>
            <IconButton
              ref={editRef}
              icon="pencil"
              variant="ghost"
              size="sm"
              label={`Edit "${activity.summary}"`}
              disabled={pending}
              onClick={() => {
                setRowError(null);
                setMode("edit");
              }}
            />
            <IconButton
              icon="trash-2"
              variant="danger"
              size="sm"
              label={`Cancel "${activity.summary}"`}
              disabled={pending}
              onClick={() => void cancel()}
            />
          </div>
        )}
      </div>
      {mode === "done" && (
        <div className="flex items-center gap-2 ps-7">
          <div className="flex-1">
            <TextInput
              size="sm"
              value={feedback}
              onChange={setFeedback}
              onKeyDown={onFeedbackKeyDown}
              placeholder="Feedback (optional)"
              aria-label={`Feedback for "${activity.summary}"`}
              maxLength={10000}
              disabled={pending}
              autoFocus
            />
          </div>
          <ActionButton size="sm" loading={pending} disabled={pending} onClick={() => void submitDone()}>
            Done
          </ActionButton>
          <Button
            variant="ghost"
            size="sm"
            disabled={pending}
            onClick={() => {
              setMode("view");
              setReturnFocus("markDone");
            }}
          >
            Cancel
          </Button>
        </div>
      )}
      {rowError && (
        <p role="alert" className="ps-7 text-danger text-sm">
          {rowError}
        </p>
      )}
    </li>
  );
}

interface ActivityFormValues {
  type: string;
  dueDate: string;
  summary: string;
  assignee: RecordReader | null;
  note: string;
}

interface ActivityFormProps {
  model: string;
  recordId: string;
  activityTypes: ActivityTypesLookup;
  today: string;
  initial: ActivityFormValues;
  submitLabel: string;
  autoFocusField: "type" | "summary";
  onSubmit: (values: ActivityFormValues) => Promise<void>;
  // Returns true when it handled the error itself.
  onError?: ((error: unknown) => boolean) | undefined;
  onCancel: () => void;
}

// DateField reads and writes date-only values as UTC midnight.
function toDateFieldValue(date: string): Date | undefined {
  return date ? new Date(`${date}T00:00:00Z`) : undefined;
}

function ActivityForm({
  model,
  recordId,
  activityTypes,
  today,
  initial,
  submitLabel,
  autoFocusField,
  onSubmit,
  onError,
  onCancel,
}: ActivityFormProps): ReactNode {
  const [values, setValues] = useState(initial);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<keyof ActivityFormValues, string>>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const formRef = useRef<HTMLFormElement | null>(null);
  // Once the admin types by hand into summary or due date, picking a
  // different type stops overwriting that field (scheduled-activities.md
  // §9 "Using a type").
  const touchedRef = useRef<Set<"summary" | "dueDate">>(new Set());

  // A FieldWrapper gives its control the wrapper's own id, so fields are found by their wrapping element.
  function focusField(name: keyof ActivityFormValues): void {
    formRef.current?.querySelector<HTMLElement>(`[data-field="${name}"] :is(input, button, textarea)`)?.focus();
  }

  // biome-ignore lint/correctness/useExhaustiveDependencies: focuses the first field once, on open.
  useEffect(() => {
    focusField(autoFocusField);
  }, []);

  function set<K extends keyof ActivityFormValues>(key: K, value: ActivityFormValues[K]): void {
    setValues((current) => ({ ...current, [key]: value }));
    setFieldErrors((current) => ({ ...current, [key]: undefined }));
    if (key === "summary" || key === "dueDate") touchedRef.current.add(key);
  }

  function setType(type: string): void {
    const defaults = activityTypes.getType(type);
    setValues((current) => ({
      ...current,
      type,
      summary:
        !touchedRef.current.has("summary") && defaults?.defaultSummary ? defaults.defaultSummary : current.summary,
      dueDate:
        !touchedRef.current.has("dueDate") && defaults?.defaultDueDays !== undefined && defaults.defaultDueDays !== null
          ? dueDateFromDefault(today, defaults.defaultDueDays)
          : current.dueDate,
    }));
    setFieldErrors(({ type: _type, ...rest }) => rest);
  }

  // Active types plus the form's current value, so an activity keeping an
  // already-archived type doesn't vanish from its own Select.
  const typeOptions = activityTypes.types.filter((t) => !t.archived || t.key === values.type);

  async function submit(event: SubmitEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (submitting) return;
    const summary = values.summary.trim();
    const errors: typeof fieldErrors = {};
    if (summary === "") errors.summary = "Enter a summary.";
    if (values.dueDate === "") errors.dueDate = "Choose a due date.";
    if (errors.summary || errors.dueDate) {
      setFieldErrors(errors);
      focusField(errors.dueDate ? "dueDate" : "summary");
      return;
    }
    setFormError(null);
    setSubmitting(true);
    try {
      await onSubmit({ ...values, summary, note: values.note.trim() });
    } catch (error) {
      const code = error instanceof AppError ? error.code : undefined;
      if (code === "invalid_assignee") {
        const name = values.assignee?.name ?? values.assignee?.email ?? "This person";
        setFieldErrors({ assignee: `${name} can't see this record anymore. Choose someone else.` });
      } else if (code === "invalid_type") {
        setFieldErrors({ type: "This type is no longer available. Choose another." });
      } else if (!onError?.(error)) {
        setFormError(error instanceof Error && error.message ? error.message : "Couldn't save this activity.");
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form ref={formRef} onSubmit={(event) => void submit(event)} className="flex flex-col gap-3" noValidate>
      <div className="grid gap-3 sm:grid-cols-2">
        <div data-field="type">
          <FieldWrapper label="Type" required error={fieldErrors.type}>
            <Select
              options={typeOptions.map((t) => ({ value: t.key, label: t.label, icon: t.icon }))}
              value={values.type}
              onChange={(value) => setType(value as string)}
            />
          </FieldWrapper>
        </div>
        <div data-field="dueDate">
          <FieldWrapper label="Due date" required error={fieldErrors.dueDate}>
            <DateField
              value={toDateFieldValue(values.dueDate)}
              onChange={(date) => set("dueDate", date ? date.toISOString().slice(0, 10) : "")}
            />
          </FieldWrapper>
        </div>
        <div data-field="summary" className="sm:col-span-2">
          <FieldWrapper label="Summary" required error={fieldErrors.summary}>
            <TextInput value={values.summary} onChange={(value) => set("summary", value)} maxLength={200} />
          </FieldWrapper>
        </div>
        <div className="sm:col-span-2">
          <FieldWrapper label="Assignee" required error={fieldErrors.assignee}>
            <RecordReaderPicker
              model={model}
              recordId={recordId}
              value={values.assignee}
              onChange={(reader) => set("assignee", reader)}
            />
          </FieldWrapper>
        </div>
        <div className="sm:col-span-2">
          <FieldWrapper label="Note">
            <TextArea value={values.note} onChange={(value) => set("note", value)} rows={3} maxLength={10000} />
          </FieldWrapper>
        </div>
      </div>
      {formError && (
        <p role="alert" className="text-danger text-sm">
          {formError}
        </p>
      )}
      <div className="flex justify-end gap-2">
        <Button type="submit" variant="primary" size="sm" loading={submitting}>
          {submitLabel}
        </Button>
        <Button variant="ghost" size="sm" disabled={submitting} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
