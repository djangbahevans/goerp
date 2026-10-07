import { ActionButton, AlertDialog, Button, Icon, PageHeader, PageLayout, Skeleton } from "@goerp/sdk/components";
import { moduleLink } from "@goerp/sdk/nav";
import { recordActivityQueryKey } from "@goerp/sdk/react";
import { viewPathRegistry } from "@goerp/sdk/schema";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { useConditionEvaluator } from "../../conditions/use-condition-evaluator.js";
import { ListActions } from "../list/list-actions.js";
import { FormChatter } from "./form-chatter.js";
import { FormSectionRenderer } from "./form-sections.js";
import { ShareHeaderAction } from "./form-share-action.js";
import { FormSidebarRenderer } from "./form-sidebar.js";
import { FormTabsRenderer } from "./form-tabs.js";
import type { FormViewDeclaration } from "./form-view-types.js";
import { WorkflowActions } from "./form-workflow-actions.js";
import { useCanUpdateRecord } from "./use-can-update-record.js";
import type { FormLeaveGuard } from "./use-form-leave-guard.js";
import { useFormLeaveGuard } from "./use-form-leave-guard.js";
import { useFormMode } from "./use-form-mode.js";
import type { UseFormRecordOptions } from "./use-form-record.js";
import { recordQueryKey, useFormRecord } from "./use-form-record.js";

// The first control a user can type into: the hidden native <select> behind a
// Select is aria-hidden and out of the tab order, so it never matches.
const FIRST_EDITABLE_SELECTOR =
  'input:not([type="hidden"]):not([disabled]):not([aria-hidden="true"]), textarea:not([disabled]), button[role="combobox"]:not([disabled]), [contenteditable="true"]';

// shell-architecture.md §20's FormRenderer.
export interface FormRendererProps {
  view: FormViewDeclaration;
  module: string;
  recordId?: string;
  // Test-only: threads through to useFormRecord's own registry/client/
  // autoSaveDelay seam, so a story can exercise a real save mutation's
  // isSaving/saveError states without a live backend or a real multi-
  // second autosave debounce. Never set at a real call site.
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
  const modeRef = useRef<{ leaveEdit: () => void }>({ leaveEdit: () => {} });
  const guardRef = useRef<Pick<FormLeaveGuard, "unguarded">>({ unguarded: (navigation) => navigation() });
  const { record, isLoading, isError, error, refetch, isDirty, setField, reset, save, isSaving, saveError } =
    useFormRecord(view.resource, recordId, {
      autoSave: view.autosave ?? false,
      // A save may write a change entry to the chatter's feed, which
      // otherwise only refetches after the viewer's own comment or delete.
      onSaved: (saved) => {
        if (recordId !== undefined) {
          void queryClient.invalidateQueries({ queryKey: recordActivityQueryKey(view.resource, recordId) });
          if (!view.autosave) modeRef.current.leaveEdit();
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
  const leaveGuard = useFormLeaveGuard(isEditing && isDirty && !view.autosave);
  guardRef.current = leaveGuard;
  const [announcement, setAnnouncement] = useState("");
  const editButtonRef = useRef<HTMLButtonElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);

  // Display mode shows every field as a value, which is what a read-only form already does.
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
    if (!isEditing || recordId === undefined || discarding || leaveGuard.blocked) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented) requestCancel();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  });

  const cancelEdit = () => {
    reset();
    leaveEdit();
    setAnnouncement("");
  };
  const requestCancel = () => (isDirty ? setDiscarding(true) : cancelEdit());

  if (isLoading) {
    return (
      <PageLayout>
        <Skeleton />
      </PageLayout>
    );
  }

  // list-renderer.md's own shared "record/list load failed" treatment —
  // one design, two call sites.
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
      {/* A defined Fragment even when both actions render null —
          PageHeader's empty-actions skip won't fire, but the resulting
          empty wrapper is harmless. */}
      <PageHeader
        title={view.label}
        actions={
          <>
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
          {/* view-system.md §9's EmploymentTab convention for grouping
              multiple SectionCards (section-card.md's own Existing
              Patterns table). */}
          <div className="space-y-4">
            {view.sections.map((section, i) => (
              // No stable `name` guaranteed; safe since this only reorders on reload.
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

        {view.sidebar && <FormSidebarRenderer sidebar={view.sidebar} record={record} />}
      </div>

      {!view.autosave && isEditing && (
        // "Structure stays put" the way AlertDialog's own fixed button row
        // does, at a smaller scale — sticky, not scrolled away with a long
        // field list.
        <footer className="sticky bottom-0 flex items-center gap-4 border-border border-t bg-bg p-4">
          <ActionButton variant="primary" loading={isSaving} disabled={!isDirty || formReadonly} onClick={save}>
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
      <AlertDialog
        open={discarding}
        title="Discard changes?"
        description="Your unsaved changes to this record will be lost."
        tone="warning"
        confirmLabel="Discard"
        confirmVariant="danger"
        onCancel={() => setDiscarding(false)}
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
        onConfirm={leaveGuard.leave}
      />
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
