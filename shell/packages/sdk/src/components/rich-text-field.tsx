import * as PopoverPrimitive from "@radix-ui/react-popover";
import { EditorContent, useEditor, useEditorState } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import {
  Bold,
  ExternalLink,
  Heading1,
  Heading2,
  Heading3,
  Italic,
  Link as LinkIcon,
  List,
  Quote,
  Redo2,
  Trash2,
  Underline,
  Undo2,
} from "lucide-react";
import type { ReactNode } from "react";
import { useEffect, useId, useMemo, useState } from "react";
import { actionButtonClassName } from "./action-button-styles.js";
import { fieldInputClassName } from "./field-input-styles.js";

export interface RichTextFieldProps {
  label?: string | undefined;
  // External label target — contentEditable isn't natively labelable, so
  // <label htmlFor> can't reach it (goerp#698).
  ariaLabelledBy?: string | undefined;
  // HTML string, per rich-text-field.md's resolved storage-format question
  // (not Markdown — a separate manifest field type — and not a structured
  // document tree, which would be the only field in this library not
  // storing a plain scalar).
  value: string;
  onChange: (value: string) => void;
  // manifest-spec.md's FormField.rows ("Valid for textarea, rich_text") —
  // applied as an approximate min-height on the editable area, the closest
  // equivalent a contentEditable region has to a <textarea>'s rows.
  rows?: number | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

// The extension's own default opens any clicked link in a new tab even
// inside an editable document — not the right behavior for an editor whose
// own Link button needs a plain click on link text to place the cursor.
const EXTENSIONS = [StarterKit.configure({ link: { openOnClick: false } })];

const TOOLBAR_ICON_SIZE = 16;
const TOOLBAR_BUTTON_CLASSES = actionButtonClassName("ghost", "sm");

const HEADING_LEVELS = [1, 2, 3] as const;
const HEADING_ICONS = { 1: Heading1, 2: Heading2, 3: Heading3 } as const;

function toolbarButtonClassName(active: boolean): string {
  return `${TOOLBAR_BUTTON_CLASSES} ${active ? "bg-surface-active text-primary" : ""}`;
}

interface ToolbarButtonProps {
  label: string;
  active: boolean;
  disabled: boolean;
  onClick: () => void;
  children: ReactNode;
}

function ToolbarButton({ label, active, disabled, onClick, children }: ToolbarButtonProps): ReactNode {
  return (
    <button
      type="button"
      aria-label={label}
      aria-pressed={active}
      disabled={disabled}
      onClick={onClick}
      className={toolbarButtonClassName(active)}
    >
      {children}
    </button>
  );
}

function ToolbarDivider(): ReactNode {
  return <div aria-hidden="true" className="mx-1 h-5 w-px bg-border" />;
}

interface MomentaryToolbarButtonProps {
  label: string;
  disabled: boolean;
  onClick: () => void;
  children: ReactNode;
}

// Undo/Redo, unlike ToolbarButton's other callers, are momentary actions
// with no persistent "on" state — no aria-pressed, since their disabled
// state is the whole affordance (matching a native app's undo/redo).
function MomentaryToolbarButton({ label, disabled, onClick, children }: MomentaryToolbarButtonProps): ReactNode {
  return (
    <button type="button" aria-label={label} disabled={disabled} onClick={onClick} className={TOOLBAR_BUTTON_CLASSES}>
      {children}
    </button>
  );
}

export function RichTextField({
  label,
  ariaLabelledBy,
  value,
  onChange,
  rows = 3,
  error,
  disabled = false,
}: RichTextFieldProps): ReactNode {
  const id = useId();
  const linkInputId = `${id}-link-url`;
  const labelledById = ariaLabelledBy ?? (label !== undefined ? id : undefined);
  const [linkPopoverOpen, setLinkPopoverOpen] = useState(false);
  const [linkUrl, setLinkUrl] = useState("");
  const [linkUrlInvalid, setLinkUrlInvalid] = useState(false);

  // Memoized so this object's reference only changes when one of these
  // values actually does — useEditor re-syncs options on every render it's
  // given a new options object for (no explicit `deps`), and a fresh
  // literal here would otherwise trigger that sync on every keystroke.
  const editorProps = useMemo(
    () => ({
      attributes: {
        role: "textbox",
        "aria-multiline": "true",
        ...(labelledById !== undefined ? { "aria-labelledby": labelledById } : {}),
        "aria-invalid": String(error !== undefined),
      },
    }),
    [labelledById, error],
  );

  const editor = useEditor({
    extensions: EXTENSIONS,
    content: value,
    editable: !disabled,
    editorProps,
    onUpdate: ({ editor: e }) => onChange(e.getHTML()),
  });

  // useEditor only forces a re-render on document changes (onUpdate), not
  // on selection/stored-mark-only transactions — toggling Bold at a
  // collapsed cursor with no text to wrap is exactly that case, so the
  // toolbar's aria-pressed state needs its own subscription to every
  // transaction to stay in sync.
  const activeMarks = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      bold: e?.isActive("bold") ?? false,
      italic: e?.isActive("italic") ?? false,
      underline: e?.isActive("underline") ?? false,
      bulletList: e?.isActive("bulletList") ?? false,
      blockquote: e?.isActive("blockquote") ?? false,
      heading1: e?.isActive("heading", { level: 1 }) ?? false,
      heading2: e?.isActive("heading", { level: 2 }) ?? false,
      heading3: e?.isActive("heading", { level: 3 }) ?? false,
      link: e?.isActive("link") ?? false,
      linkHref: (e?.getAttributes("link").href as string | undefined) ?? "",
      selectionEmpty: e?.state.selection.empty ?? true,
      canUndo: e?.can().undo() ?? false,
      canRedo: e?.can().redo() ?? false,
    }),
  });

  // `setEditable`'s own emitUpdate defaults to true, which would fire
  // onChange on every disabled toggle with no actual content change —
  // `false` here is required, not optional.
  useEffect(() => {
    editor?.setEditable(!disabled, false);
  }, [editor, disabled]);

  // Syncs an externally-changed value (e.g. switching records) without
  // clobbering in-progress typing — only fires when the editor's own HTML
  // actually diverges from the prop. `emitUpdate: false` is required here
  // too, for the same reason as setEditable above.
  useEffect(() => {
    if (editor && editor.getHTML() !== value) editor.commands.setContent(value, { emitUpdate: false });
  }, [editor, value]);

  if (!editor) return null;

  const openLinkPopover = () => {
    setLinkUrl(activeMarks.linkHref);
    setLinkUrlInvalid(false);
    setLinkPopoverOpen(true);
  };

  const applyLink = () => {
    const href = linkUrl.trim();
    if (href === "") return;
    // extendMarkRange covers the edit-existing-link case: the cursor sits
    // collapsed inside the link's text, and plain setLink (unlike
    // unsetLink, which passes extendEmptyMarkRange itself) only writes to
    // stored marks on a collapsed selection otherwise.
    const applied = editor.chain().focus().extendMarkRange("link").setLink({ href }).run();
    // setLink returns false without throwing when the URL fails the
    // extension's own protocol allowlist — surfaced rather than silently
    // closing the popover as though it had applied.
    if (applied) setLinkPopoverOpen(false);
    else setLinkUrlInvalid(true);
  };

  const removeLink = () => {
    editor.chain().focus().unsetLink().run();
    setLinkPopoverOpen(false);
  };

  const headingActive: Record<(typeof HEADING_LEVELS)[number], boolean> = {
    1: activeMarks.heading1,
    2: activeMarks.heading2,
    3: activeMarks.heading3,
  };

  // Applying a link to a collapsed selection is a silent no-op (nothing to
  // wrap in an <a>) — disabled here instead of letting a user type a URL
  // that visibly does nothing on submit, unless a link is already active
  // (editing/removing an existing one needs no selection).
  const linkButtonDisabled = disabled || (!activeMarks.link && activeMarks.selectionEmpty);

  return (
    <div className="flex flex-col gap-1">
      {label !== undefined && (
        <span id={id} className="text-sm text-text">
          {label}
        </span>
      )}
      <div className={fieldInputClassName(error !== undefined, "wrapper", "sans")}>
        <div
          role="toolbar"
          aria-label="Formatting"
          className="flex flex-wrap items-center gap-1 border-border border-b pb-2"
        >
          <MomentaryToolbarButton
            label="Undo"
            disabled={disabled || !activeMarks.canUndo}
            onClick={() => editor.chain().focus().undo().run()}
          >
            <Undo2 size={TOOLBAR_ICON_SIZE} />
          </MomentaryToolbarButton>
          <MomentaryToolbarButton
            label="Redo"
            disabled={disabled || !activeMarks.canRedo}
            onClick={() => editor.chain().focus().redo().run()}
          >
            <Redo2 size={TOOLBAR_ICON_SIZE} />
          </MomentaryToolbarButton>
          <ToolbarDivider />
          {HEADING_LEVELS.map((level) => {
            const HeadingIcon = HEADING_ICONS[level];
            return (
              <ToolbarButton
                key={level}
                label={`Heading ${level}`}
                active={headingActive[level]}
                disabled={disabled}
                onClick={() => editor.chain().focus().toggleHeading({ level }).run()}
              >
                <HeadingIcon size={TOOLBAR_ICON_SIZE} />
              </ToolbarButton>
            );
          })}
          <ToolbarDivider />
          <ToolbarButton
            label="Bold"
            active={activeMarks.bold}
            disabled={disabled}
            onClick={() => editor.chain().focus().toggleBold().run()}
          >
            <Bold size={TOOLBAR_ICON_SIZE} />
          </ToolbarButton>
          <ToolbarButton
            label="Italic"
            active={activeMarks.italic}
            disabled={disabled}
            onClick={() => editor.chain().focus().toggleItalic().run()}
          >
            <Italic size={TOOLBAR_ICON_SIZE} />
          </ToolbarButton>
          <ToolbarButton
            label="Underline"
            active={activeMarks.underline}
            disabled={disabled}
            onClick={() => editor.chain().focus().toggleUnderline().run()}
          >
            <Underline size={TOOLBAR_ICON_SIZE} />
          </ToolbarButton>
          <ToolbarDivider />
          <ToolbarButton
            label="Bulleted list"
            active={activeMarks.bulletList}
            disabled={disabled}
            onClick={() => editor.chain().focus().toggleBulletList().run()}
          >
            <List size={TOOLBAR_ICON_SIZE} />
          </ToolbarButton>
          <ToolbarButton
            label="Blockquote"
            active={activeMarks.blockquote}
            disabled={disabled}
            onClick={() => editor.chain().focus().toggleBlockquote().run()}
          >
            <Quote size={TOOLBAR_ICON_SIZE} />
          </ToolbarButton>
          <ToolbarDivider />
          <PopoverPrimitive.Root open={linkPopoverOpen && !disabled} onOpenChange={setLinkPopoverOpen}>
            <PopoverPrimitive.Trigger asChild>
              <button
                type="button"
                aria-label="Link"
                aria-pressed={activeMarks.link}
                disabled={linkButtonDisabled}
                onClick={openLinkPopover}
                className={toolbarButtonClassName(activeMarks.link)}
              >
                <LinkIcon size={TOOLBAR_ICON_SIZE} />
              </button>
            </PopoverPrimitive.Trigger>
            <PopoverPrimitive.Portal>
              <PopoverPrimitive.Content
                sideOffset={4}
                className="z-(--z-dropdown) flex flex-col gap-1 rounded-structural border border-border bg-surface p-2 shadow-md"
              >
                <div className="flex items-center gap-1">
                  <label htmlFor={linkInputId} className="sr-only">
                    Link URL
                  </label>
                  <input
                    id={linkInputId}
                    type="url"
                    placeholder="Paste a link…"
                    value={linkUrl}
                    aria-invalid={linkUrlInvalid}
                    onChange={(e) => {
                      setLinkUrl(e.target.value);
                      setLinkUrlInvalid(false);
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault();
                        applyLink();
                      }
                    }}
                    className={`w-56 rounded-control border px-2 py-1 text-sm text-text focus-visible:outline-none focus-visible:shadow-focus ${linkUrlInvalid ? "border-danger" : "border-border"}`}
                  />
                  {activeMarks.link && (
                    <a
                      href={activeMarks.linkHref}
                      target="_blank"
                      rel="noopener noreferrer"
                      aria-label="Open link in new tab"
                      className={actionButtonClassName("ghost", "sm")}
                    >
                      <ExternalLink size={TOOLBAR_ICON_SIZE} />
                    </a>
                  )}
                  {activeMarks.link && (
                    <button
                      type="button"
                      aria-label="Remove link"
                      onClick={removeLink}
                      className={actionButtonClassName("ghost", "sm")}
                    >
                      <Trash2 size={TOOLBAR_ICON_SIZE} />
                    </button>
                  )}
                </div>
                {linkUrlInvalid && (
                  <span role="alert" className="text-danger text-xs">
                    Enter a valid URL
                  </span>
                )}
              </PopoverPrimitive.Content>
            </PopoverPrimitive.Portal>
          </PopoverPrimitive.Root>
        </div>
        <EditorContent editor={editor} className="pt-2" style={{ minHeight: `${rows * 1.5}em` }} />
      </div>
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
    </div>
  );
}
