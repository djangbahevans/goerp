import { AuthContext } from "@goerp/sdk/auth";
import {
  ActionButton,
  Button,
  EmptyState,
  formatFieldValue,
  Icon,
  IconButton,
  SectionCard,
  Skeleton,
  TextArea,
  Timeline,
  TimelineItem,
} from "@goerp/sdk/components";
import type { ActivityEntry } from "@goerp/sdk/react";
import { useConfirm, useRecordActivity, useRelationLabels } from "@goerp/sdk/react";
import type { FieldDef } from "@goerp/sdk/schema";
import { modelRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useContext, useEffect, useId, useMemo, useRef, useState } from "react";
import { IS_MAC } from "../../shortcuts/shortcut.js";
import { type ChangeLabelContext, changeRelationSpecs, collectFormFields, entryDisplay } from "./chatter-entries.js";
import type { FormViewDeclaration } from "./form-view-types.js";

// docs/components/form-chatter.md.
export interface FormChatterProps {
  view: FormViewDeclaration;
  recordId: string | undefined;
}

export function FormChatter({ view, recordId }: FormChatterProps) {
  if (recordId === undefined) {
    return (
      <SectionCard title="Activity">
        <p className="text-sm text-text-secondary">Activity appears here once this record is saved.</p>
      </SectionCard>
    );
  }
  return <ChatterPanel view={view} recordId={recordId} />;
}

function useChangeLabelContext(view: FormViewDeclaration, entries: ActivityEntry[]): ChangeLabelContext {
  const formFields = useMemo(() => collectFormFields(view), [view]);
  const { data: model } = useQuery({
    queryKey: ["form-chatter-model", view.resource],
    queryFn: () => modelRegistry.resolve(view.resource),
  });
  const modelFields = useMemo(
    () => new Map<string, FieldDef>((model?.fields ?? []).map((field) => [field.name, field])),
    [model],
  );
  const relationLabels = useRelationLabels(changeRelationSpecs(entries, formFields, modelFields));
  return { formFields, modelFields, relationLabels };
}

function ChatterPanel({ view, recordId }: { view: FormViewDeclaration; recordId: string }) {
  const activity = useRecordActivity(view.resource, recordId);
  const { entries, isLoading, isError, error, hasMore, isFetchingNextPage } = activity;
  const labelContext = useChangeLabelContext(view, entries);
  const currentUserId = useContext(AuthContext)?.user?.id;
  const { confirm } = useConfirm();

  const [announcement, setAnnouncement] = useState("");
  const [deleteErrors, setDeleteErrors] = useState<ReadonlySet<string>>(new Set());
  const [focusEntryId, setFocusEntryId] = useState<string | null>(null);
  // The entry count when "Load more" was pressed, until that fetch settles.
  const [loadMore, setLoadMore] = useState<{ from: number; started: boolean } | null>(null);
  const [loadMoreFailed, setLoadMoreFailed] = useState(false);
  const itemRefs = useRef(new Map<string, HTMLLIElement>());

  const unsupported = isError && error?.code === "activity_unsupported";
  useEffect(() => {
    if (unsupported && import.meta.env.DEV) {
      console.warn(
        `FormChatter: "${view.resource}" has no activity feed (Virtual or Transient model). Set "chatter": false on form view "${view.name}".`,
      );
    }
  }, [unsupported, view.resource, view.name]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: `entries` re-runs the lookup once the refetched entry has rendered.
  useEffect(() => {
    if (focusEntryId === null) return;
    const item = itemRefs.current.get(focusEntryId);
    if (item) {
      item.focus();
      setFocusEntryId(null);
    }
  }, [focusEntryId, entries]);

  // Settles a "Load more" once its fetch has started and finished, however
  // it ended, so a later refetch is never mistaken for it.
  useEffect(() => {
    if (loadMore === null) return;
    if (isFetchingNextPage) {
      if (!loadMore.started) setLoadMore({ ...loadMore, started: true });
      return;
    }
    if (!loadMore.started) return;
    setLoadMore(null);
    if (isError) {
      setLoadMoreFailed(true);
      return;
    }
    const loaded = entries.length - loadMore.from;
    if (loaded <= 0) return;
    setAnnouncement(`${loaded} more ${loaded === 1 ? "entry" : "entries"} loaded`);
    if (!hasMore) setFocusEntryId(entries[loadMore.from]?.id ?? null);
  }, [loadMore, isFetchingNextPage, isError, entries, hasMore]);

  useEffect(() => {
    if (!isError) setLoadMoreFailed(false);
  }, [isError]);

  if (unsupported) return null;

  async function handleDelete(entry: ActivityEntry) {
    const confirmed = await confirm({
      title: "Delete this comment?",
      description: `It's replaced with a "Comment deleted" marker for everyone. This can't be undone.`,
      confirmLabel: "Delete",
      cancelLabel: "Cancel",
      variant: "danger",
    });
    if (!confirmed) return;
    setDeleteErrors((current) => withoutId(current, entry.id));
    try {
      await activity.deleteComment(entry.id);
      setFocusEntryId(entry.id);
    } catch {
      setDeleteErrors((current) => new Set(current).add(entry.id));
    }
  }

  function renderFeed() {
    if (isLoading) return <Skeleton lines={3} />;
    if (entries.length === 0 && isError) {
      return (
        <div role="alert" className="flex flex-col items-center gap-2 py-4 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-sm text-text">Couldn't load activity.</p>
          <ActionButton variant="secondary" size="sm" onClick={activity.refetch}>
            Retry
          </ActionButton>
        </div>
      );
    }
    if (entries.length === 0) {
      return (
        <EmptyState
          size="compact"
          title="No activity yet"
          description="Comments and changes to tracked fields show up here."
        />
      );
    }
    return (
      <>
        <Timeline>
          {entries.map((entry) => {
            const display = entryDisplay(entry, labelContext);
            const ownComment =
              entry.kind === "comment" &&
              !entry.deleted &&
              currentUserId !== undefined &&
              entry.author?.id === currentUserId;
            return (
              <TimelineItem
                key={entry.id}
                ref={(item: HTMLLIElement | null) => {
                  if (item) itemRefs.current.set(entry.id, item);
                  else itemRefs.current.delete(entry.id);
                }}
                tabIndex={-1}
                icon={<Icon name={display.icon} size={16} className="text-text-secondary" />}
                title={display.title}
                timestamp={entry.createdAt}
                user={
                  entry.author
                    ? { name: entry.author.name ?? "Unknown user", avatarUrl: entry.author.avatarUrl }
                    : undefined
                }
                actions={
                  ownComment ? (
                    <IconButton
                      icon="trash-2"
                      variant="danger"
                      size="sm"
                      label={`Delete comment from ${formatFieldValue(entry.createdAt, "datetime", undefined, "")}`}
                      disabled={activity.deletingIds.includes(entry.id)}
                      onClick={() => void handleDelete(entry)}
                    />
                  ) : undefined
                }
              >
                {display.body !== undefined || deleteErrors.has(entry.id) ? (
                  <>
                    {display.body}
                    {deleteErrors.has(entry.id) && (
                      <p role="alert" className="mt-1 text-sm text-danger">
                        Couldn't delete this comment.
                      </p>
                    )}
                  </>
                ) : undefined}
              </TimelineItem>
            );
          })}
        </Timeline>
        {isError && (
          <p role="alert" className="mt-4 text-center text-sm text-danger">
            {loadMoreFailed ? "Couldn't load more activity." : "Couldn't refresh activity."}
          </p>
        )}
        {hasMore && (
          <div className="mt-4 flex justify-center">
            <ActionButton
              variant="secondary"
              size="sm"
              loading={isFetchingNextPage}
              onClick={() => {
                setLoadMore({ from: entries.length, started: false });
                setLoadMoreFailed(false);
                activity.fetchMore();
              }}
            >
              Load more
            </ActionButton>
          </div>
        )}
      </>
    );
  }

  return (
    <SectionCard title="Activity">
      <div className="space-y-4">
        <ChatterComposer
          isPosting={activity.isPosting}
          onPost={async (body) => {
            await activity.postComment(body);
            setAnnouncement("Comment posted");
          }}
        />
        <div>{renderFeed()}</div>
      </div>
      <p role="status" className="sr-only">
        {announcement}
      </p>
    </SectionCard>
  );
}

function withoutId(ids: ReadonlySet<string>, id: string): ReadonlySet<string> {
  if (!ids.has(id)) return ids;
  const next = new Set(ids);
  next.delete(id);
  return next;
}

const POST_SHORTCUT_HINT = IS_MAC ? "⌘ Enter to post" : "Ctrl + Enter to post";

function ChatterComposer({ isPosting, onPost }: { isPosting: boolean; onPost: (body: string) => Promise<void> }) {
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const canPost = text.trim() !== "" && !isPosting;

  async function submit() {
    if (!canPost) return;
    setError(null);
    try {
      await onPost(text);
      setText("");
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : "Couldn't post the comment.");
    }
  }

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <TextArea
        value={text}
        onChange={(value) => {
          setText(value);
          setError(null);
        }}
        rows={3}
        maxLength={10000}
        placeholder="Add a comment…"
        aria-label="Add a comment"
        aria-describedby={error ? `${hintId} ${errorId}` : hintId}
        invalid={error !== null}
        readOnly={isPosting}
        onKeyDown={(event) => {
          if (event.key !== "Enter" || !(IS_MAC ? event.metaKey : event.ctrlKey)) return;
          event.preventDefault();
          void submit();
        }}
      />
      {error && (
        <p id={errorId} role="alert" className="mt-1 text-sm text-danger">
          {error}
        </p>
      )}
      <div className="mt-2 flex items-center justify-between gap-2">
        <span id={hintId} className="text-text-secondary text-xs">
          {POST_SHORTCUT_HINT}
        </span>
        <Button type="submit" variant="primary" size="sm" loading={isPosting} disabled={!canPost}>
          Comment
        </Button>
      </div>
    </form>
  );
}
