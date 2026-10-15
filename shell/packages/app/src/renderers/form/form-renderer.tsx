import { ActionButton, AlertDialog, Button, Icon, PageHeader, PageLayout, Skeleton } from "@goerp/sdk/components";
import { moduleLink } from "@goerp/sdk/nav";
import { recordActivityQueryKey } from "@goerp/sdk/react";
import { useExecutionContext, useOnline } from "@goerp/sdk/realtime";
import { viewPathRegistry } from "@goerp/sdk/schema";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { useConditionEvaluator } from "../../conditions/use-condition-evaluator.js";
import { ListActions } from "../list/list-actions.js";
import { FormChatter } from "./form-chatter.js";
import { FormCollaborators, FormConflictWarning } from "./form-collaboration.js";
import { FormSectionRenderer } from "./form-sections.js";
import { ShareHeaderAction } from "./form-share-action.js";
import { FormSidebarRenderer } from "./form-sidebar.js";
import { FormTabsRenderer } from "./form-tabs.js";
import { FormUnavailable } from "./form-unavailable.js";
import type { FormViewDeclaration } from "./form-view-types.js";
import { WorkflowActions } from "./form-workflow-actions.js";
import { useReportUnsavedChanges } from "./unsaved-changes.js";
import { useCanUpdateRecord } from "./use-can-update-record.js";
import type { FormLeaveGuard } from "./use-form-leave-guard.js";
import { useFormLeaveGuard } from "./use-form-leave-guard.js";
import type { FormModeHandle } from "./use-form-mode.js";
import { useFormMode } from "./use-form-mode.js";
import type { UseFormRecordOptions } from "./use-form-record.js";
import { recordQueryKey, useFormRecord } from "./use-form-record.js";

// The first control a user can type into: the hidden native <select> behind a
// Select is aria-hidden and out of the tab order, so it never matches.
const FIRST_EDITABLE_SELECTOR =
  'input:not([type="hidden"]):not([disabled]):not([aria-hidden="true"]), textarea:not([disabled]), button[role="combobox"]:not([disabled]), [contenteditable="true"]';

function fieldOf(target: EventTarget | null): string | null {
  return target instanceof Element ? (target.closest("[data-field]")?.getAttribute("data-field") ?? null) : null;
}

export interface FormRendererProps {
  view: FormViewDeclaration;
  module: string;
  recordId?: string;
  testFormRecordOptions?: Pick<UseFormRecordOptions, "registry" | "client" | "autoSaveDelay">;
}

export function FormRenderer({ view, module, recordId, testFormRecordOptions }: FormRendererProps) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const online = useOnline();
  // Offline, autosave pauses and keeps the edits; they save once the connection returns.
  const autoSave = (view.autosave ?? false) && online;
  const modeRef = useRef<Pick<FormModeHandle, "leaveEdit">>({ leaveEdit: async () => {} });
  const guardRef = useRef<Pick<FormLeaveGuard, "unguarded">>({ unguarded: (navigation) => navigation() });
  const {
    record,
    isLoading,
    isError,
    error,
    refetch,
    isDirty,
    isUnavailable,
    unsavedEdits,
    setField,
    reset,
    save,
    isSaving,
    saveError,
    hasConflict,
    keepLocalEdits,
    resolveConflict,
  } = useFormRecord(view.resource, recordId, {
    autoSave,
    // A save may write a change entry to the chatter's feed, which
    // otherwise only refetches after the viewer's own comment or delete.
    onSaved: (saved) => {
      if (recordId !== undefined) {
        void queryClient.invalidateQueries({ queryKey: recordActivityQueryKey(view.resource, recordId) });
        if (!view.autosave) void guardRef.current.unguarded(() => modeRef.current.leaveEdit());
        return;
      }
      // A create form has no record in its URL; move to the new record's,
      // so a second save updates it instead of creating another.
      const createdId = saved.id;
      if (typeof createdId !== "string") return;
      viewPathRegistry
        .resolveRecord(view.name, module)
        .then(async (path) => {
          if (!path || !mounted.current) return;
          await guardRef.current.unguarded(() =>
            navigate({ to: moduleLink(path.replace("{id}", createdId)), replace: true }),
          );
          // Otherwise the next "New" form would open prefilled with this record.
          queryClient.removeQueries({ queryKey: recordQueryKey(view.resource, undefined) });
        })
        .catch(() => {});
    },
    ...testFormRecordOptions,
  });

  useExecutionContext({
    view: "form",
    resource: view.resource,
    recordId: recordId ?? null,
    record,
    queryKey: recordId !== undefined ? recordQueryKey(view.resource, recordId) : null,
  });

  const conditions = useConditionEvaluator(`form "${view.name}"`);
  const formReadonly = conditions.isReadonly(view.readonly_condition, "readonly_condition", record);
  const { mode, edit, leaveEdit } = useFormMode({
    isNew: recordId === undefined,
    autosave: view.autosave ?? false,
    readonly: formReadonly,
  });
  modeRef.current.leaveEdit = leaveEdit;
  const canUpdate = useCanUpdateRecord(view.resource);
  const [discarding, setDiscarding] = useState(false);
  const isEditing = mode === "edit";
  const holdsUnsavedChanges = isDirty && (isUnavailable || (isEditing && !autoSave));
  const leaveGuard = useFormLeaveGuard(holdsUnsavedChanges);
  useReportUnsavedChanges(holdsUnsavedChanges);
  guardRef.current = leaveGuard;
  const [announcement, setAnnouncement] = useState("");
  const [focusedField, setFocusedField] = useState<string | null>(null);
  const editButtonRef = useRef<HTMLButtonElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const unavailableHeadingRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    const content = contentRef.current;
    if (!content) return;
    const onFocusIn = (event: FocusEvent) => setFocusedField(fieldOf(event.target));
    const onFocusOut = (event: FocusEvent) => {
      if (!(event.relatedTarget instanceof Node) || !content.contains(event.relatedTarget)) setFocusedField(null);
    };
    content.addEventListener("focusin", onFocusIn);
    content.addEventListener("focusout", onFocusOut);
    return () => {
      content.removeEventListener("focusin", onFocusIn);
      content.removeEventListener("focusout", onFocusOut);
    };
  });

  const fieldsReadonly = formReadonly || !isEditing;
  const canEdit = !formReadonly && canUpdate && recordId !== undefined && !view.autosave;

  // Focus follows the mode: the first editable field on Edit, the Edit button after Save or Cancel.
  const previousMode = useRef(mode);
  useEffect(() => {
    if (previousMode.current === mode) return;
    previousMode.current = mode;
    if (mode === "edit") {
      contentRef.current?.querySelector<HTMLElement>(FIRST_EDITABLE_SELECTOR)?.focus();
      setAnnouncement("Editing");
    } else {
      editButtonRef.current?.focus();
      setAnnouncement("Saved");
    }
  }, [mode]);

  // Escape closes an open popover or menu first, which marks the event handled; only an unhandled one cancels the edit.
  useEffect(() => {
    if (isUnavailable || !isEditing || recordId === undefined || discarding || leaveGuard.blocked) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented) requestCancel();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  });

  const cancelEdit = () => {
    reset();
    if (!isUnavailable) void leaveGuard.unguarded(leaveEdit);
    setAnnouncement("");
  };
  const requestCancel = () => (isDirty ? setDiscarding(true) : cancelEdit());

  const dialogs = (
    <>
      <AlertDialog
        open={discarding}
        title="Discard changes?"
        description="Your unsaved changes to this record will be lost."
        tone="warning"
        confirmLabel="Discard"
        confirmVariant="danger"
        onCancel={() => setDiscarding(false)}
        onClosed={() => {
          if (isUnavailable) unavailableHeadingRef.current?.focus();
        }}
        onConfirm={() => {
          setDiscarding(false);
          cancelEdit();
        }}
      />
      <AlertDialog
        open={leaveGuard.blocked}
        title="Leave without saving?"
        description="Your unsaved changes to this record will be lost."
        tone="warning"
        confirmLabel="Leave"
        cancelLabel="Stay"
        confirmVariant="danger"
        onCancel={leaveGuard.stay}
        onConfirm={() => {
          leaveGuard.leave();
          reset();
        }}
      />
    </>
  );

  if (isUnavailable) {
    return (
      <PageLayout>
        <FormUnavailable
          label={view.label}
          edits={unsavedEdits}
          onDiscard={() => setDiscarding(true)}
          heading={unavailableHeadingRef}
        />
        {dialogs}
      </PageLayout>
    );
  }

  if (isLoading) {
    return (
      <PageLayout>
        <Skeleton />
      </PageLayout>
    );
  }

  if (isError) {
    return (
      <PageLayout>
        <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-text">Couldn't load {view.label}.</p>
          {error && <p className="text-sm text-text-secondary">{error.message}</p>}
          <ActionButton variant="secondary" onClick={refetch}>
            Retry
          </ActionButton>
        </div>
      </PageLayout>
    );
  }

  return (
    <PageLayout>
      <PageHeader
        title={view.label}
        actions={
          <>
            {recordId !== undefined && (
              <FormCollaborators
                resource={view.resource}
                recordId={recordId}
                editingField={isEditing ? focusedField : null}
              />
            )}
            {canEdit && !isEditing && (
              <Button ref={editButtonRef} variant="secondary" onClick={() => edit()}>
                Edit
              </Button>
            )}
            {view.workflow_actions && <WorkflowActions resource={view.resource} recordId={recordId} record={record} />}
            <ListActions actions={view.header_actions ?? []} module={module} record={record} viewName={view.name} />
            <ShareHeaderAction resource={view.resource} recordId={recordId} />
          </>
        }
      />

      {/* Not TwoColumnLayout — FormSidebar.width is per-view configurable,
          incompatible with its fixed 2/3+1/3 ratio; gap-8 (2rem) kept in sync with it.
          Below 768px the sidebar stacks under the content. */}
      <div className="flex flex-col gap-8 md:flex-row">
        <div ref={contentRef} className="min-w-0 flex-1 space-y-4">
          {isEditing && hasConflict && (
            <FormConflictWarning onKeepLocal={keepLocalEdits} onLoadSaved={resolveConflict} />
          )}
          <div className="space-y-4">
            {view.sections.map((section, i) => (
              <FormSectionRenderer
                key={section.name ?? i}
                section={section}
                resource={view.resource}
                module={module}
                record={record}
                recordId={recordId}
                onChange={setField}
                formReadonly={fieldsReadonly}
              />
            ))}
          </div>

          {/* No `view.tabs.length` gate — a view extension (view-system.md
              §10) can add a tab even when this view declares none of its
              own; FormTabsRenderer itself renders null once both its own
              and extension tabs resolve to nothing visible. */}
          <FormTabsRenderer
            tabs={view.tabs ?? []}
            resource={view.resource}
            module={module}
            viewName={view.name}
            record={record}
            recordId={recordId}
            onChange={setField}
            formReadonly={fieldsReadonly}
          />

          {view.chatter !== false && <FormChatter view={view} recordId={recordId} />}
        </div>

        {view.sidebar && <FormSidebarRenderer sidebar={view.sidebar} record={record} resource={view.resource} />}
      </div>

      {!view.autosave && isEditing && (
        <footer className="sticky bottom-0 flex items-center gap-4 border-border border-t bg-bg p-4">
          <ActionButton
            variant="primary"
            loading={isSaving}
            disabled={!isDirty || formReadonly || !online}
            onClick={save}
          >
            Save
          </ActionButton>
          {recordId !== undefined && (
            <ActionButton variant="ghost" onClick={requestCancel}>
              Cancel
            </ActionButton>
          )}
          {saveError && (
            <span role="alert" className="text-danger text-sm">
              {saveError.message}
            </span>
          )}
        </footer>
      )}
      {dialogs}
      <span role="status" className="sr-only">
        {announcement}
      </span>
      {view.autosave && isSaving && (
        <span role="status" className="text-sm text-text-secondary">
          Saving…
        </span>
      )}
      {view.autosave && saveError && (
        <span role="alert" className="text-danger text-sm">
          {saveError.message}
        </span>
      )}
    </PageLayout>
  );
}
