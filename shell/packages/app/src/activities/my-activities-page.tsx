import {
  ActionButton,
  Button,
  EmptyState,
  Icon,
  PageHeader,
  PageLayout,
  Skeleton,
  TextInput,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { type MyScheduledActivity, useMyActivities } from "@goerp/sdk/react";
import { type KeyboardEvent, type ReactNode, useId, useState } from "react";
import { DUE_GROUP_LABELS, dueLabel, groupByDue, todayIn } from "./activity-dates.js";
import { activityTypeDisplay } from "./activity-types.js";
import { RecordFormLink } from "./record-form-link.js";
import { MY_ACTIVITIES_PAGE_SIZE, useActivityTimezone } from "./use-due-activity-count.js";

function markDoneErrorMessage(error: unknown): string {
  if (error instanceof AppError) {
    if (error.code === "activity_done") return "This activity was already marked done.";
    if (error.code === "not_participant" || error.code === "permission_denied") {
      return "You can no longer change this activity.";
    }
  }
  return "Couldn't mark this activity done. Try again.";
}

// shell-ux.md §8: the caller's open activities, grouped Overdue, Today and
// Upcoming by their own timezone, each completable inline.
export function MyActivitiesPage(): ReactNode {
  const timeZone = useActivityTimezone();
  const { activities, isLoading, isError, hasMore, fetchMore, isFetchingNextPage, refetch, markDone, pendingIds } =
    useMyActivities({ limit: MY_ACTIVITIES_PAGE_SIZE });
  // Completed rows leave at once rather than waiting for the refetch.
  const [completedIds, setCompletedIds] = useState<readonly string[]>([]);
  const today = todayIn(timeZone);

  const complete = async (activity: MyScheduledActivity, feedback: string) => {
    const trimmed = feedback.trim();
    try {
      await markDone(activity.id, trimmed ? { feedback: trimmed } : undefined);
      setCompletedIds((ids) => [...ids, activity.id]);
      return true;
    } catch (error) {
      toast.error(markDoneErrorMessage(error));
      refetch();
      return false;
    }
  };

  const visible = activities.filter((a) => !completedIds.includes(a.id));

  let body: ReactNode;
  if (isLoading) {
    body = <Skeleton lines={6} />;
  } else if (isError) {
    body = (
      <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
        <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
        <p className="text-text">Couldn't load your activities.</p>
        <ActionButton variant="secondary" onClick={refetch}>
          Retry
        </ActionButton>
      </div>
    );
  } else if (visible.length === 0 && !hasMore) {
    body = (
      <EmptyState
        icon="calendar-check"
        title="Nothing planned"
        description="Activities assigned to you on any record will show up here."
      />
    );
  } else {
    body = (
      <div className="flex flex-col gap-6">
        {groupByDue(visible, today).map(({ group, items }) => (
          <ActivityGroup key={group} title={DUE_GROUP_LABELS[group]}>
            {items.map((activity) => (
              <ActivityRow
                key={activity.id}
                activity={activity}
                today={today}
                pending={pendingIds.includes(activity.id)}
                onComplete={(feedback) => complete(activity, feedback)}
              />
            ))}
          </ActivityGroup>
        ))}
        {hasMore && (
          <div className="flex justify-center">
            <ActionButton variant="secondary" loading={isFetchingNextPage} onClick={fetchMore}>
              Load more
            </ActionButton>
          </div>
        )}
      </div>
    );
  }

  return (
    <PageLayout>
      <PageHeader title="My activities" subtitle="Calls, meetings, emails, and to-dos assigned to you." />
      {body}
    </PageLayout>
  );
}

function ActivityGroup({ title, children }: { title: string; children: ReactNode }): ReactNode {
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-2">
      <h2 id={headingId} className="font-semibold text-sm text-text-secondary">
        {title}
      </h2>
      <ul className="flex flex-col divide-y divide-border rounded-structural border border-border bg-surface">
        {children}
      </ul>
    </section>
  );
}

function ActivityRow({
  activity,
  today,
  pending,
  onComplete,
}: {
  activity: MyScheduledActivity;
  today: string;
  pending: boolean;
  onComplete: (feedback: string) => Promise<boolean>;
}): ReactNode {
  const [completing, setCompleting] = useState(false);
  const [feedback, setFeedback] = useState("");
  const type = activityTypeDisplay(activity.type);
  const overdue = activity.dueDate < today;
  const recordLabel = activity.recordName ?? activity.recordId;
  const due = dueLabel(activity.dueDate, today);
  const dueClassName = overdue ? "text-danger" : "text-text-secondary";

  const submit = async () => {
    if (await onComplete(feedback)) {
      setCompleting(false);
      setFeedback("");
    }
  };

  // While a completion is in flight, neither key starts a second one.
  const onFeedbackKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      if (!pending) void submit();
    } else if (event.key === "Escape" && !pending) {
      event.stopPropagation();
      setCompleting(false);
    }
  };

  return (
    <li className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-start gap-3">
        <span className="mt-0.5 flex-none text-text-secondary" title={type.label}>
          <Icon name={type.icon} size={16} aria-hidden="true" />
          <span className="sr-only">{type.label}</span>
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="wrap-break-word text-sm text-text">{activity.summary}</span>
          <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-sm">
            <span className="min-w-0 truncate">
              <RecordFormLink model={activity.model} recordId={activity.recordId}>
                {recordLabel}
              </RecordFormLink>
            </span>
            {/* Below the sm breakpoint the date shares this line, leaving the summary room to wrap. */}
            <span className={`sm:hidden ${dueClassName}`}>{due}</span>
          </span>
        </div>
        <span className={`hidden flex-none text-sm sm:block ${dueClassName}`}>{due}</span>
        {!completing && (
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setCompleting(true)}
            aria-label={`Mark "${activity.summary}" done`}
          >
            Mark done
          </Button>
        )}
      </div>
      {completing && (
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
              autoFocus
            />
          </div>
          <ActionButton size="sm" loading={pending} disabled={pending} onClick={() => void submit()}>
            Done
          </ActionButton>
          <Button variant="ghost" size="sm" disabled={pending} onClick={() => setCompleting(false)}>
            Cancel
          </Button>
        </div>
      )}
    </li>
  );
}
