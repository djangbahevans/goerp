import {
  Button,
  Checkbox,
  EscapeLayer,
  Skeleton,
  TextArea,
  useFloatingPanelLayer,
  useFloatingPanelPosition,
  useListboxNavigation,
  useOutsideClickClose,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { type RecordReader, useRecordReaders } from "@goerp/sdk/react";
import { type KeyboardEvent, type ReactNode, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { IS_MAC } from "../../shortcuts/shortcut.js";
import {
  continueMentionQuery,
  encodeMentions,
  type MentionQuery,
  mentionName,
  mentionQueryStartedAt,
} from "./chatter-mentions.js";
import { RecordReaderOption, recordReaderOptionLabel } from "./record-reader-picker.js";

const POST_SHORTCUT_HINT = IS_MAC ? "⌘ Enter to post" : "Ctrl + Enter to post";
const MAX_CANDIDATES = 8;
const MENTION_LIST_MAX_WIDTH = 320;

// docs/components/form-chatter.md "Composer": the comment box, its mention
// autocomplete and the "Notify followers" option.
export interface ChatterComposerProps {
  model: string;
  recordId: string;
  isPosting: boolean;
  // Followers other than the viewer, or null while unknown.
  otherFollowers: number | null;
  onPost: (body: string, notifyFollowers: boolean) => Promise<void>;
  announce: (text: string) => void;
}

// "A", "A and B", "A, B and C".
function joinNames(names: string[]): string {
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

export function ChatterComposer({
  model,
  recordId,
  isPosting,
  otherFollowers,
  onPost,
  announce,
}: ChatterComposerProps): ReactNode {
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notify, setNotify] = useState(false);
  const [mention, setMention] = useState<MentionQuery | null>(null);
  // Each chosen "@{name}" → the user it mentions, forgotten after a post.
  const [recorded, setRecorded] = useState<ReadonlyMap<string, RecordReader>>(new Map());
  const textAreaRef = useRef<HTMLTextAreaElement | null>(null);
  const wrapperRef = useRef<HTMLDivElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const pendingCaret = useRef<number | null>(null);
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const canPost = text.trim() !== "" && !isPosting;
  // A disabled checkbox reads and posts as unchecked, even if it was
  // checked before the other followers went away.
  const canNotify = otherFollowers !== null && otherFollowers > 0;
  const notifyFollowers = notify && canNotify;

  const listOpen = mention !== null;
  const { readers, isLoading, isError } = useRecordReaders(model, recordId, mention?.query ?? null, {
    excludeSelf: true,
  });
  const shown = isError ? [] : readers.slice(0, MAX_CANDIDATES);
  // Nothing is highlighted while the list is loading, empty or failed, so
  // Enter and Tab keep their usual meaning then.
  const choosable = isLoading ? 0 : shown.length;
  const nav = useListboxNavigation({
    count: choosable,
    isOpen: listOpen,
    homeEnd: false,
    onChoose: (index) => {
      const reader = shown[index];
      if (reader) choose(reader);
    },
  });

  const position = useFloatingPanelPosition(listOpen, wrapperRef, panelRef, false);
  const layerClassName = useFloatingPanelLayer(wrapperRef);
  useOutsideClickClose(listOpen, [wrapperRef, panelRef], () => setMention(null));

  useLayoutEffect(() => {
    const caret = pendingCaret.current;
    if (caret === null) return;
    pendingCaret.current = null;
    textAreaRef.current?.setSelectionRange(caret, caret);
  });

  // biome-ignore lint/correctness/useExhaustiveDependencies: the query re-announces an unchanged count for a new search; `announce` is recreated every render.
  useEffect(() => {
    if (!listOpen || isLoading) return;
    if (isError) announce("Couldn't load people.");
    else if (shown.length === 0) announce("No one found who can see this record.");
    else announce(`${shown.length} ${shown.length === 1 ? "person" : "people"} found`);
  }, [listOpen, isLoading, isError, shown.length, mention?.query]);

  function choose(reader: RecordReader): void {
    if (mention === null) return;
    const name = mentionName(reader);
    const end = mention.start + 1 + mention.query.length;
    const inserted = `@${name} `;
    setText(text.slice(0, mention.start) + inserted + text.slice(end));
    pendingCaret.current = mention.start + inserted.length;
    setRecorded((current) => new Map(current).set(name, reader));
    setMention(null);
  }

  function handleChange(value: string): void {
    const caret = textAreaRef.current?.selectionStart ?? value.length;
    const started = value.length > text.length ? mentionQueryStartedAt(value, caret) : null;
    const next = started ?? (mention ? continueMentionQuery(value, mention.start, caret) : null);
    if (next?.query !== mention?.query || next?.start !== mention?.start) nav.setActiveIndex(0);
    setMention(next);
    setText(value);
    setError(null);
  }

  // A caret moved without typing ends the query once it leaves it.
  function handleSelect(): void {
    const caret = textAreaRef.current?.selectionStart;
    if (caret === undefined) return;
    setMention((current) =>
      current && caret >= current.start + 1 && caret <= current.start + 1 + current.query.length ? current : null,
    );
  }

  function postError(err: unknown): string {
    if (err instanceof AppError && err.code === "invalid_mention") {
      const byId = new Map([...recorded.values()].map((reader) => [reader.id, mentionName(reader)]));
      const ids = Array.isArray(err.details?.user_ids) ? (err.details.user_ids as string[]) : [];
      const names = ids.map((userId) => byId.get(userId) ?? "A mentioned user");
      return `${joinNames(names.length > 0 ? names : ["A mentioned user"])} can't see this record, so they can't be mentioned. Remove the mention and post again.`;
    }
    return err instanceof Error && err.message ? err.message : "Couldn't post the comment.";
  }

  async function submit(): Promise<void> {
    if (!canPost) return;
    setError(null);
    setMention(null);
    const mentions = new Map([...recorded].map(([name, reader]) => [name, reader.id]));
    try {
      await onPost(encodeMentions(text, mentions), notifyFollowers);
      setText("");
      setRecorded(new Map());
      setNotify(false);
    } catch (err) {
      setError(postError(err));
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>): void {
    if (event.key === "Enter" && (IS_MAC ? event.metaKey : event.ctrlKey)) {
      event.preventDefault();
      void submit();
      return;
    }
    if (!listOpen || event.nativeEvent.isComposing) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp" || event.key === "Enter") {
      nav.handleKeyDown(event);
      return;
    }
    if (event.key !== "Tab") return;
    const reader = choosable > 0 ? shown[nav.activeIndex] : undefined;
    if (reader) {
      event.preventDefault();
      choose(reader);
    } else {
      setMention(null);
    }
  }

  function renderList(): ReactNode {
    if (shown.length === 0) {
      if (isLoading) return <Skeleton lines={2} />;
      return isError ? (
        <p className="px-3 py-2 text-danger text-sm">Couldn't load people.</p>
      ) : (
        <p className="px-3 py-2 text-sm text-text-secondary">No one found who can see this record.</p>
      );
    }
    return shown.map((reader, index) => {
      const label = recordReaderOptionLabel(reader, undefined);
      const highlighted = choosable > 0 && index === nav.activeIndex;
      return (
        // biome-ignore lint/a11y/useAriaPropsSupportedByRole: role="option" comes from getOptionProps.
        <div
          key={reader.id}
          {...nav.getOptionProps(index, { selected: highlighted })}
          aria-label={label}
          title={label}
          className={`flex cursor-pointer items-center rounded-control px-3 py-2 ${highlighted ? "bg-surface-hover" : ""}`}
        >
          <RecordReaderOption reader={reader} viewerId={undefined} />
        </div>
      );
    });
  }

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <div ref={wrapperRef}>
        <TextArea
          ref={textAreaRef}
          value={text}
          onChange={handleChange}
          onSelect={handleSelect}
          rows={3}
          maxLength={10000}
          placeholder="Add a comment… Type @ to mention someone"
          aria-label="Add a comment"
          aria-describedby={error ? `${hintId} ${errorId}` : hintId}
          aria-autocomplete="list"
          aria-controls={listOpen ? nav.listboxId : undefined}
          aria-activedescendant={listOpen ? nav.inputProps["aria-activedescendant"] : undefined}
          invalid={error !== null}
          readOnly={isPosting}
          onKeyDown={handleKeyDown}
        />
      </div>
      {listOpen &&
        createPortal(
          <EscapeLayer onEscape={() => setMention(null)}>
            <div
              ref={panelRef}
              id={nav.listboxId}
              role="listbox"
              aria-label="People who can see this record"
              aria-busy={isLoading || undefined}
              style={
                position
                  ? {
                      position: "fixed",
                      top: position.top,
                      left: position.left,
                      width: Math.min(position.width, MENTION_LIST_MAX_WIDTH),
                    }
                  : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
              }
              className={`${layerClassName} max-h-80 overflow-y-auto rounded-structural border border-border bg-surface p-1 shadow-md`}
            >
              {renderList()}
            </div>
          </EscapeLayer>,
          document.body,
        )}
      {error && (
        <p id={errorId} role="alert" className="mt-1 text-sm text-danger">
          {error}
        </p>
      )}
      <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
        <Checkbox
          checked={notifyFollowers}
          onChange={setNotify}
          label={otherFollowers === null ? "Notify followers" : `Notify followers (${otherFollowers})`}
          disabled={!canNotify}
        />
        <div className="ml-auto flex items-center gap-2">
          <span id={hintId} className="text-text-secondary text-xs">
            {POST_SHORTCUT_HINT}
          </span>
          <Button type="submit" variant="primary" size="sm" loading={isPosting} disabled={!canPost}>
            Comment
          </Button>
        </div>
      </div>
    </form>
  );
}
