import { useTenant } from "@goerp/sdk/auth";
import {
  ActionButton,
  AlertDialog,
  Badge,
  Button,
  Checkbox,
  CodeField,
  FieldWrapper,
  Icon,
  IconButton,
  LanguageSelect,
  Select,
  Skeleton,
  TabPanel,
  Tabs,
  TextArea,
  TextInput,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { SideSheet } from "../../chrome/side-sheet.js";
import { NotificationPreviewPanel, useNotificationPreview } from "./notification-preview-panel.js";
import {
  CHANNEL_COLUMNS,
  type NotificationTemplate,
  type TemplateChannel,
  type TemplateColumn,
  type TemplateFields,
  type TemplateTarget,
  type TemplateType,
  type TemplateVariable,
  useNotificationTemplate,
  useNotificationTemplates,
  useResetNotificationTemplate,
  useSaveNotificationTemplate,
} from "./notification-templates-api.js";
import { CHANNELS, COLUMN_LABELS, channelLabel, fallbackLocale, languageName, rowKey } from "./template-rows.js";

export interface NotificationTemplateSheetProps {
  open: boolean;
  // Called once the sheet has decided to close: after a save or reset, or a
  // close request with no unsaved changes or a confirmed discard.
  onClose: () => void;
  // null opens the sheet in add mode.
  target: TemplateTarget | null;
  // Pre-selects the notification type in add mode.
  initialType?: string | undefined;
}

type Picks = { type: string; channel: TemplateChannel | ""; locale: string };

// shell-ux.md §5.8 "Template editor (sheet)".
export function NotificationTemplateSheet({
  open,
  onClose,
  target,
  initialType,
}: NotificationTemplateSheetProps): ReactNode {
  const tenant = useTenant();
  const types = useNotificationTemplates().data ?? [];
  const [picks, setPicks] = useState<Picks>({ type: "", channel: "", locale: "" });
  const [dirty, setDirty] = useState(false);
  const [discardOpen, setDiscardOpen] = useState(false);
  const closeAfterDialog = useRef(false);

  // biome-ignore lint/correctness/useExhaustiveDependencies: resets only when the sheet opens.
  useEffect(() => {
    if (!open) return;
    setPicks({ type: initialType ?? "", channel: "", locale: "" });
    setDirty(false);
  }, [open]);

  const resolved: TemplateTarget | null =
    target ?? (picks.type && picks.channel && picks.locale ? { ...picks, channel: picks.channel } : null);
  const declared = resolved ? types.find((t) => t.type === resolved.type) : undefined;
  const exists =
    target !== null ||
    (resolved !== null &&
      (declared?.templates.some((e) => e.channel === resolved.channel && e.locale === resolved.locale) ?? false));

  function requestClose(): void {
    if (dirty) setDiscardOpen(true);
    else onClose();
  }

  const pickedType = types.find((t) => t.type === picks.type);

  return (
    <SideSheet open={open} onClose={requestClose} title={exists ? "Edit template" : "Add template"} width="wide">
      <div className="flex flex-col gap-4 p-4">
        {target === null && (
          <div className="grid gap-4 min-[960px]:grid-cols-3">
            <FieldWrapper label="Notification">
              <Select
                value={picks.type}
                placeholder="Choose a notification…"
                options={types
                  .toSorted((a, b) => a.label.localeCompare(b.label))
                  .map((t) => ({ value: t.type, label: t.label }))}
                onChange={(value) => {
                  const type = types.find((t) => t.type === value);
                  setPicks((current) => ({
                    ...current,
                    type: String(value),
                    channel: type?.availableChannels.includes(current.channel as TemplateChannel)
                      ? current.channel
                      : "",
                  }));
                }}
              />
            </FieldWrapper>
            <FieldWrapper label="Channel">
              <Select
                value={picks.channel}
                placeholder="Choose a channel…"
                disabled={!pickedType}
                options={CHANNELS.filter((c) => pickedType?.availableChannels.includes(c.channel)).map((c) => ({
                  value: c.channel,
                  label: c.label,
                  icon: c.icon,
                }))}
                onChange={(value) => setPicks((current) => ({ ...current, channel: String(value) as TemplateChannel }))}
              />
            </FieldWrapper>
            <FieldWrapper label="Language">
              <LanguageSelect
                value={picks.locale}
                tags={tenant.availableLocales}
                placeholder="Choose a language…"
                onChange={(locale) => setPicks((current) => ({ ...current, locale }))}
              />
            </FieldWrapper>
          </div>
        )}
        {resolved && declared && (
          <TemplateEditor
            key={rowKey(resolved.type, resolved.channel, resolved.locale)}
            target={resolved}
            declared={declared}
            types={types}
            isNew={!exists}
            onDirtyChange={setDirty}
            onDone={onClose}
          />
        )}
      </div>
      <AlertDialog
        open={discardOpen}
        title="Discard your changes?"
        description="Your edits to this template will be lost."
        confirmLabel="Discard"
        cancelLabel="Keep editing"
        confirmVariant="danger"
        onConfirm={() => {
          closeAfterDialog.current = true;
          setDiscardOpen(false);
        }}
        onCancel={() => setDiscardOpen(false)}
        onClosed={() => {
          // Closing the sheet only once the dialog has returned focus lets
          // SideSheet then hand focus back to the sheet's trigger.
          if (!closeAfterDialog.current) return;
          closeAfterDialog.current = false;
          onClose();
        }}
      />
    </SideSheet>
  );
}

type Draft = Record<TemplateColumn, string>;

function draftFrom(channel: TemplateChannel, fields: TemplateFields | null | undefined): Draft {
  return Object.fromEntries(CHANNEL_COLUMNS[channel].map((column) => [column, fields?.[column] ?? ""])) as Draft;
}

function sameDraft(channel: TemplateChannel, a: Draft, b: Draft): boolean {
  return CHANNEL_COLUMNS[channel].every((column) => a[column] === b[column]);
}

type SampleValues = Record<string, string | boolean>;

function sampleData(variables: TemplateVariable[], values: SampleValues): Record<string, unknown> {
  const data: Record<string, unknown> = {};
  for (const variable of variables) {
    if (variable.source !== "data") continue;
    const value = values[variable.name];
    if (variable.type === "bool") {
      data[variable.name] = value ?? true;
    } else if (typeof value === "string" && value.trim() !== "") {
      const number = Number(value);
      if (variable.type === "string") data[variable.name] = value;
      else if (variable.type === "int" ? Number.isInteger(number) : Number.isFinite(number)) {
        data[variable.name] = number;
      }
    }
  }
  return data;
}

interface TemplateEditorProps {
  target: TemplateTarget;
  declared: TemplateType;
  types: TemplateType[];
  isNew: boolean;
  onDirtyChange: (dirty: boolean) => void;
  onDone: () => void;
}

function TemplateEditor({
  target,
  declared,
  types,
  isNew: isNewNow,
  onDirtyChange,
  onDone,
}: TemplateEditorProps): ReactNode {
  // Fixed when the editor opens: deleting the template from here makes the
  // combination new again, which mustn't swap the editor out mid-dialog.
  const [isNew] = useState(isNewNow);
  const query = useNotificationTemplate(target);
  const fallback = fallbackLocale(types, target.type, target.channel, target.locale);
  const fallbackQuery = useNotificationTemplate(isNew && fallback ? { ...target, locale: fallback } : null);
  // Seeded only from a fetch made since the sheet opened, so a save never
  // overwrites another admin's newer change with an edit of a stale copy.
  const ready = query.isFetchedAfterMount && (!isNew || fallback === null || fallbackQuery.isFetchedAfterMount);

  if (query.isError || fallbackQuery.isError) {
    return (
      <p role="alert" className="text-danger text-sm">
        Couldn't load this template. Close the sheet and try again.
      </p>
    );
  }
  if (!ready || !query.data) return <Skeleton lines={6} />;

  const loaded = query.data;
  const fallbackFields = fallbackQuery.data ? (fallbackQuery.data.override ?? fallbackQuery.data.default) : null;
  const initial = draftFrom(target.channel, isNew ? fallbackFields : (loaded.override ?? loaded.default));
  return (
    <LoadedEditor
      target={target}
      declared={declared}
      template={loaded}
      initial={initial}
      fallback={fallback}
      isNew={isNew}
      onDirtyChange={onDirtyChange}
      onDone={onDone}
    />
  );
}

interface LoadedEditorProps {
  target: TemplateTarget;
  declared: TemplateType;
  template: NotificationTemplate;
  initial: Draft;
  fallback: string | null;
  isNew: boolean;
  onDirtyChange: (dirty: boolean) => void;
  onDone: () => void;
}

type SideTab = "preview" | "default" | "variables";

function LoadedEditor({
  target,
  declared,
  template,
  initial,
  fallback,
  isNew,
  onDirtyChange,
  onDone,
}: LoadedEditorProps): ReactNode {
  const { channel } = target;
  const columns = CHANNEL_COLUMNS[channel];
  const save = useSaveNotificationTemplate();
  const reset = useResetNotificationTemplate();
  const [opened] = useState(initial);
  const [draft, setDraft] = useState(initial);
  const [saveErrors, setSaveErrors] = useState<Partial<Record<TemplateColumn, string>>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [focusColumn, setFocusColumn] = useState<TemplateColumn | null>(null);
  const [resetOpen, setResetOpen] = useState(false);
  const [tab, setTab] = useState<SideTab>("preview");
  const [samples, setSamples] = useState<SampleValues>({});
  const closeAfterDialog = useRef(false);
  const fieldsRef = useRef<HTMLFormElement>(null);

  const dirty = !sameDraft(channel, draft, opened);
  const hasContent = columns.some((column) => draft[column] !== "");
  const canSave = hasContent && (isNew || dirty);
  const hasOverride = template.override !== null;
  const hasDefault = template.default !== null;

  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);
  useEffect(() => () => onDirtyChange(false), [onDirtyChange]);

  const preview = useNotificationPreview(target, draft, sampleData(template.variables, samples));
  const previewError = preview.result?.kind === "template_error" ? preview.result : null;

  useEffect(() => {
    if (!focusColumn) return;
    fieldsRef.current
      ?.querySelector<HTMLElement>(`[data-column="${focusColumn}"] :is(input, textarea, [contenteditable="true"])`)
      ?.focus();
    setFocusColumn(null);
  }, [focusColumn]);

  function setColumn(column: TemplateColumn, value: string): void {
    setDraft((current) => ({ ...current, [column]: value }));
    setSaveErrors(({ [column]: _cleared, ...rest }) => rest);
  }

  function fieldError(column: TemplateColumn): string | undefined {
    const message = saveErrors[column] ?? (previewError?.field === column ? previewError.message : undefined);
    return message === undefined ? undefined : `This doesn't work as a template: ${message}`;
  }

  async function submit(): Promise<void> {
    setFormError(null);
    try {
      await save.mutateAsync({ target, fields: draft });
      toast.success("Template saved.");
      onDone();
    } catch (err) {
      const column = err instanceof AppError ? err.details?.field : undefined;
      if (err instanceof AppError && err.code === "invalid_template" && columns.includes(column as TemplateColumn)) {
        const message = typeof err.details?.message === "string" ? err.details.message : err.message;
        setSaveErrors({ [column as TemplateColumn]: message });
        setFocusColumn(column as TemplateColumn);
      } else {
        setFormError("Couldn't save this template. Try again.");
      }
    }
  }

  async function confirmReset(): Promise<void> {
    setFormError(null);
    try {
      await reset.mutateAsync({ target });
      toast.success(hasDefault ? "Template reset to the default." : "Template deleted.");
      closeAfterDialog.current = true;
    } catch {
      setFormError(
        hasDefault ? "Couldn't reset this template. Try again." : "Couldn't delete this template. Try again.",
      );
    }
    setResetOpen(false);
  }

  const resetCopy = removalCopy(target, hasDefault, fallback);

  return (
    <>
      <p className="flex flex-wrap items-center gap-2 text-sm text-text-secondary">
        <span>
          {declared.label} · {channelLabel(channel)} · {languageName(target.locale)}
        </span>
        {hasOverride && <Badge label="Customised" color="blue" />}
      </p>
      <div className="grid items-start gap-6 min-[960px]:grid-cols-[minmax(0,1fr)_360px]">
        <form
          ref={fieldsRef}
          className="flex min-w-0 flex-col gap-4"
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            if (canSave) void submit();
          }}
        >
          <ChannelFields
            channel={channel}
            draft={draft}
            onChange={setColumn}
            errorFor={fieldError}
            sms={preview.result}
          />
          {!hasContent && <p className="text-sm text-text-secondary">Add some content to save this template.</p>}
          {formError && (
            <p role="alert" className="text-danger text-sm">
              {formError}
            </p>
          )}
          <div className="flex gap-2">
            <ActionButton variant="primary" loading={save.isPending} disabled={!canSave} onClick={() => void submit()}>
              Save
            </ActionButton>
            {hasOverride && (
              <ActionButton variant="secondary" onClick={() => setResetOpen(true)}>
                {hasDefault ? "Reset to default" : "Delete template"}
              </ActionButton>
            )}
          </div>
        </form>
        {/* Sticky in the sheet's scrolling body, so the preview stays in view beside a long email body.
            Its cap leaves room for the sheet's 61px header and the body's 32px of padding, or its top
            scrolls out of view at the end of the content. */}
        <div className="flex min-w-0 flex-col min-[960px]:sticky min-[960px]:top-0 min-[960px]:max-h-[calc(100dvh-var(--space-16)-var(--space-8))] min-[960px]:overflow-y-auto">
          <Tabs
            items={[
              { id: "preview", label: "Preview" },
              { id: "default", label: "Default" },
              { id: "variables", label: "Variables" },
            ]}
            activeId={tab}
            onChange={(id) => setTab(id as SideTab)}
          >
            <TabPanel id="preview">
              <div className="pt-4">
                <NotificationPreviewPanel
                  preview={preview}
                  type={target.type}
                  typeLabel={declared.label}
                  channel={channel}
                />
              </div>
            </TabPanel>
            <TabPanel id="default">
              <DefaultTab
                channel={channel}
                fields={template.default}
                fallback={fallback}
                onCopy={() => setDraft(draftFrom(channel, template.default))}
              />
            </TabPanel>
            <TabPanel id="variables">
              <VariablesTab
                variables={template.variables}
                samples={samples}
                onSample={(name, value) => setSamples((current) => ({ ...current, [name]: value }))}
              />
            </TabPanel>
          </Tabs>
        </div>
      </div>
      <AlertDialog
        open={resetOpen}
        title={resetCopy.title}
        description={resetCopy.description}
        confirmLabel={resetCopy.confirmLabel}
        confirmVariant="danger"
        onConfirm={() => void confirmReset()}
        onCancel={() => setResetOpen(false)}
        onClosed={() => {
          if (!closeAfterDialog.current) return;
          closeAfterDialog.current = false;
          onDone();
        }}
      />
    </>
  );
}

export function removalCopy(
  target: TemplateTarget,
  hasDefault: boolean,
  fallback: string | null,
): { title: string; description: string; confirmLabel: string } {
  if (hasDefault) {
    return {
      title: "Reset to default?",
      description: "Your version is deleted. New notifications use the default.",
      confirmLabel: "Reset",
    };
  }
  const language = languageName(target.locale);
  return {
    title: "Delete this template?",
    description: fallback
      ? `Members who use ${language} get the ${languageName(fallback)} template instead.`
      : `Members who use ${language} no longer get a template of their own.`,
    confirmLabel: "Delete",
  };
}

interface ChannelFieldsProps {
  channel: TemplateChannel;
  draft: Draft;
  onChange: (column: TemplateColumn, value: string) => void;
  errorFor: (column: TemplateColumn) => string | undefined;
  sms: ReturnType<typeof useNotificationPreview>["result"];
}

function ChannelFields({ channel, draft, onChange, errorFor, sms }: ChannelFieldsProps): ReactNode {
  const text = (column: TemplateColumn, description?: string) => (
    <div data-column={column}>
      <FieldWrapper label={COLUMN_LABELS[column]} description={description} error={errorFor(column)}>
        <TextInput value={draft[column]} onChange={(value) => onChange(column, value)} />
      </FieldWrapper>
    </div>
  );
  const area = (column: TemplateColumn, rows: number) => (
    <div data-column={column}>
      <FieldWrapper label={COLUMN_LABELS[column]} error={errorFor(column)}>
        <TextArea rows={rows} value={draft[column]} onChange={(value) => onChange(column, value)} />
      </FieldWrapper>
    </div>
  );

  switch (channel) {
    case "in_app":
      return (
        <>
          {text("title_template")}
          {area("body_template", 3)}
          {text("action_url_template")}
          {text("icon", "A Lucide icon name, or a variable such as {{.TypeIcon}}.")}
        </>
      );
    case "email":
      return (
        <>
          {text("subject_template")}
          <div data-column="html_template">
            <CodeField
              label={COLUMN_LABELS.html_template}
              language="html"
              rows={12}
              value={draft.html_template}
              onChange={(value) => onChange("html_template", value)}
              error={errorFor("html_template")}
            />
          </div>
          {area("text_template", 6)}
        </>
      );
    case "sms":
      return <SMSField value={draft.sms_template} onChange={onChange} error={errorFor("sms_template")} sms={sms} />;
    case "push":
      return (
        <>
          {text("push_title_template")}
          {area("push_body_template", 3)}
        </>
      );
  }
}

function SMSField({
  value,
  onChange,
  error,
  sms,
}: {
  value: string;
  onChange: (column: TemplateColumn, value: string) => void;
  error: string | undefined;
  sms: ChannelFieldsProps["sms"];
}): ReactNode {
  const counterId = useId();
  const [counts, setCounts] = useState<{ characters: number; segments: number } | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const unknown = sms?.kind === "template_error";
  const next = sms?.kind === "rendered" ? sms.preview.sms : null;
  const empty = sms?.kind === "empty";

  useEffect(() => {
    if (empty) setCounts(null);
  }, [empty]);

  useEffect(() => {
    if (!next) return;
    if (counts && counts.segments !== next.segments) {
      setAnnouncement(next.segments === 1 ? "Now sent as 1 message." : `Now sent as ${next.segments} messages.`);
    }
    if (counts?.characters !== next.characters || counts?.segments !== next.segments) setCounts(next);
  }, [next, counts]);

  return (
    <div data-column="sms_template" className="flex flex-col gap-1">
      <FieldWrapper label={COLUMN_LABELS.sms_template} error={error}>
        <TextArea
          rows={4}
          value={value}
          onChange={(next) => onChange("sms_template", next)}
          aria-describedby={counterId}
        />
      </FieldWrapper>
      <p id={counterId} className="flex items-center gap-1 text-text-secondary text-xs">
        {unknown ? (
          "Length unknown until the template works."
        ) : counts === null ? null : counts.segments > 1 ? (
          <>
            <Icon name="triangle-alert" size={16} className="text-warning" aria-hidden="true" />
            {counts.characters} characters · Sent as {counts.segments} messages
          </>
        ) : (
          `${counts.characters} characters · 1 segment`
        )}
      </p>
      <div aria-live="polite" className="sr-only">
        {announcement}
      </div>
    </div>
  );
}

function DefaultTab({
  channel,
  fields,
  fallback,
  onCopy,
}: {
  channel: TemplateChannel;
  fields: TemplateFields | null;
  fallback: string | null;
  onCopy: () => void;
}): ReactNode {
  if (!fields) {
    return (
      <p className="pt-4 text-sm text-text-secondary">
        {fallback
          ? `No default ships in this language. Until you add one here, members who use it get the ${languageName(fallback)} template.`
          : "No default ships in this language."}
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-4 pt-4">
      {CHANNEL_COLUMNS[channel]
        .filter((column) => fields[column])
        .map((column) => (
          <div key={column} className="flex flex-col gap-1">
            <p className="font-medium text-sm text-text">{COLUMN_LABELS[column]}</p>
            <pre className="whitespace-pre-wrap break-words rounded-control bg-bg-subtle p-2 font-mono text-text text-xs">
              {fields[column]}
            </pre>
          </div>
        ))}
      <div>
        <Button variant="ghost" size="sm" onClick={onCopy}>
          Copy to editor
        </Button>
      </div>
    </div>
  );
}

function VariablesTab({
  variables,
  samples,
  onSample,
}: {
  variables: TemplateVariable[];
  samples: SampleValues;
  onSample: (name: string, value: string | boolean) => void;
}): ReactNode {
  function copy(name: string): void {
    const written = `{{.${name}}}`;
    void navigator.clipboard.writeText(written).then(
      () => toast.success(`Copied ${written}.`),
      () => toast.error(`Couldn't copy ${written}.`),
    );
  }

  return (
    <ul className="flex flex-col divide-y divide-border pt-2">
      {variables.map((variable) => {
        const written = `{{.${variable.name}}}`;
        const sample = samples[variable.name];
        return (
          <li key={variable.name} className="flex flex-col gap-2 py-3">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0">
                <p className="break-all font-mono text-sm text-text">{written}</p>
                <p className="text-text-secondary text-xs">
                  {variable.type} · {variable.source === "data" ? "Notification data" : "Added by the platform"}
                </p>
              </div>
              <IconButton icon="copy" label={`Copy ${written}`} size="sm" onClick={() => copy(variable.name)} />
            </div>
            {variable.source === "data" &&
              (variable.type === "bool" ? (
                <Checkbox
                  label={`Sample ${variable.name}`}
                  checked={sample === undefined ? true : sample === true}
                  onChange={(checked) => onSample(variable.name, checked)}
                />
              ) : (
                <TextInput
                  aria-label={`Sample ${variable.name}`}
                  placeholder={variable.type === "string" ? variable.name : variable.type === "int" ? "1" : "1.5"}
                  inputMode={variable.type === "string" ? undefined : "decimal"}
                  value={typeof sample === "string" ? sample : ""}
                  onChange={(value) => onSample(variable.name, value)}
                />
              ))}
          </li>
        );
      })}
    </ul>
  );
}
