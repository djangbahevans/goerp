import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap, indentWithTab, temporarilySetTabFocusMode } from "@codemirror/commands";
import { HighlightStyle, LanguageDescription, syntaxHighlighting } from "@codemirror/language";
import { languages } from "@codemirror/language-data";
import { Annotation, Compartment, EditorState } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { tags as highlightTags } from "@lezer/highlight";
import type { ReactNode } from "react";
import { useEffect, useId, useRef } from "react";
import { fieldInputClassName } from "./field-input-styles.js";

export interface CodeFieldProps {
  label?: string | undefined;
  // External label target — contentEditable isn't natively labelable, so
  // <label htmlFor> can't reach it (goerp#698).
  ariaLabelledBy?: string | undefined;
  value: string;
  onChange: (value: string) => void;
  // manifest-spec.md's FormField.language ("Use language for syntax
  // language") — matched against @codemirror/language-data's bundled
  // language list by name or alias (case-insensitive, e.g. "js" or
  // "javascript"); an unmatched or omitted language renders as plain text.
  language?: string | undefined;
  rows?: number | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

// docs/components/code-field.md's "Tokens Used": syntax colors are a
// separate language-aware palette, not derived from the four functional
// status colors. `invalid` is the one exception — it flags genuinely broken
// syntax rather than categorizing valid code, so it reuses --color-danger.
const SYNTAX_HIGHLIGHT_STYLE = HighlightStyle.define([
  { tag: highlightTags.keyword, color: "var(--color-syntax-keyword)" },
  { tag: [highlightTags.string, highlightTags.special(highlightTags.string)], color: "var(--color-syntax-string)" },
  { tag: highlightTags.comment, color: "var(--color-syntax-comment)", fontStyle: "italic" },
  { tag: highlightTags.number, color: "var(--color-syntax-number)" },
  {
    tag: [highlightTags.function(highlightTags.variableName), highlightTags.function(highlightTags.propertyName)],
    color: "var(--color-syntax-function)",
  },
  { tag: [highlightTags.typeName, highlightTags.className], color: "var(--color-syntax-type)" },
  { tag: highlightTags.invalid, color: "var(--color-danger)", textDecoration: "underline wavy" },
]);

// The wrapper div (not this) owns the bordered box, so cm-scroller's own
// hard-coded generic "monospace" is overridden to inherit --font-mono
// instead, and the default focus outline is dropped in favor of the
// wrapper's own focus-within ring (fieldInputClassName).
const EDITOR_THEME = EditorView.theme({
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": { fontFamily: "inherit" },
});

// Marks the value-sync effect's own dispatch so the updateListener below can
// tell it apart from a real edit — otherwise every external value change
// (e.g. switching records) would round-trip straight back into onChange.
const externalValueSync = Annotation.define<boolean>();

export function CodeField({
  label,
  ariaLabelledBy,
  value,
  onChange,
  language,
  rows = 3,
  error,
  disabled = false,
}: CodeFieldProps): ReactNode {
  const id = useId();
  const labelledById = ariaLabelledBy ?? (label !== undefined ? id : undefined);
  const hostRef = useRef<HTMLDivElement | null>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  // Lazy ref init: `useRef(new Compartment())` would evaluate `new
  // Compartment()` (three times) on every render before useRef discards all
  // but the first, since unlike useState's lazy initializer form it always
  // evaluates its argument eagerly.
  const compartmentsRef = useRef<{ language: Compartment; editable: Compartment; attributes: Compartment } | null>(
    null,
  );
  compartmentsRef.current ??= {
    language: new Compartment(),
    editable: new Compartment(),
    attributes: new Compartment(),
  };
  const {
    language: languageCompartment,
    editable: editableCompartment,
    attributes: attributesCompartment,
  } = compartmentsRef.current;

  // Mounted once. value/language/disabled/error/label changes are applied
  // imperatively via the effects below rather than by recreating the view,
  // which would otherwise reset undo history and cursor position on every
  // keystroke (value is fed back in as a prop by a controlled caller).
  // biome-ignore lint/correctness/useExhaustiveDependencies: intentionally mount-once — value/language/disabled/error/label are synced imperatively by the effects below instead of by recreating the view.
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: value,
        extensions: [
          history(),
          closeBrackets(),
          syntaxHighlighting(SYNTAX_HIGHLIGHT_STYLE),
          EditorView.lineWrapping,
          EDITOR_THEME,
          languageCompartment.of([]),
          editableCompartment.of([EditorView.editable.of(!disabled), EditorState.readOnly.of(disabled)]),
          attributesCompartment.of(
            EditorView.contentAttributes.of({
              ...(labelledById !== undefined ? { "aria-labelledby": labelledById } : {}),
              "aria-invalid": String(error !== undefined),
            }),
          ),
          keymap.of([
            // CodeMirror's own accessibility convention (referenced in
            // docs/components/code-field.md's Accessibility section):
            // Escape temporarily suspends Tab/Shift-Tab capture so a
            // keyboard user can then Tab out, despite indentWithTab below
            // otherwise claiming Tab for indentation.
            { key: "Escape", run: temporarilySetTabFocusMode },
            ...closeBracketsKeymap,
            ...defaultKeymap,
            ...historyKeymap,
            indentWithTab,
          ]),
          EditorView.updateListener.of((update) => {
            if (update.docChanged && !update.transactions.some((tr) => tr.annotation(externalValueSync))) {
              onChangeRef.current(update.state.doc.toString());
            }
          }),
        ],
      }),
    });
    viewRef.current = view;
    return () => {
      view.destroy();
      viewRef.current = null;
    };
  }, []);

  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: editableCompartment.reconfigure([EditorView.editable.of(!disabled), EditorState.readOnly.of(disabled)]),
    });
  }, [disabled, editableCompartment]);

  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: attributesCompartment.reconfigure(
        EditorView.contentAttributes.of({
          ...(labelledById !== undefined ? { "aria-labelledby": labelledById } : {}),
          "aria-invalid": String(error !== undefined),
        }),
      ),
    });
  }, [labelledById, error, attributesCompartment]);

  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const description =
      language === undefined ? null : LanguageDescription.matchLanguageName(languages, language, true);
    if (description === null) {
      view.dispatch({ effects: languageCompartment.reconfigure([]) });
      return;
    }
    let cancelled = false;
    description.load().then((support) => {
      if (!cancelled && viewRef.current) {
        viewRef.current.dispatch({ effects: languageCompartment.reconfigure(support) });
      }
    });
    return () => {
      cancelled = true;
    };
  }, [language, languageCompartment]);

  // Syncs an externally-changed value (e.g. switching records) without
  // clobbering in-progress typing — only fires when the editor's own doc
  // actually diverges from the prop, which excludes the update this same
  // effect's own onChange call round-trips back in as a re-render.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    if (view.state.doc.toString() !== value) {
      view.dispatch({
        changes: { from: 0, to: view.state.doc.length, insert: value },
        annotations: externalValueSync.of(true),
      });
    }
  }, [value]);

  return (
    <div className="flex flex-col gap-1">
      {label !== undefined && (
        <span id={id} className="text-sm text-text">
          {label}
        </span>
      )}
      <div
        // fieldInputClassName's disabled styling keys off a `:disabled`
        // descendant (has-[:disabled]) — CodeMirror's contentEditable host
        // is never one, unlike RichTextField's real disabled toolbar
        // buttons, so disabled dimming is applied directly here instead.
        className={`${fieldInputClassName(error !== undefined, "wrapper")} ${disabled ? "cursor-not-allowed opacity-50" : ""}`}
      >
        <div ref={hostRef} style={{ minHeight: `${rows * 1.5}em` }} />
      </div>
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
    </div>
  );
}
