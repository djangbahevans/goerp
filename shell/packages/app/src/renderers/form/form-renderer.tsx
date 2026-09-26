import { ActionButton, Icon, PageHeader, PageLayout, Skeleton } from "@goerp/sdk/components";
import { recordActivityQueryKey } from "@goerp/sdk/react";
import { useQueryClient } from "@tanstack/react-query";
import { useConditionEvaluator } from "../../conditions/use-condition-evaluator.js";
import { ListActions } from "../list/list-actions.js";
import { FormChatter } from "./form-chatter.js";
import { FormSectionRenderer } from "./form-sections.js";
import { ShareHeaderAction } from "./form-share-action.js";
import { FormSidebarRenderer } from "./form-sidebar.js";
import { FormTabsRenderer } from "./form-tabs.js";
import type { FormViewDeclaration } from "./form-view-types.js";
import { WorkflowActions } from "./form-workflow-actions.js";
import type { UseFormRecordOptions } from "./use-form-record.js";
import { useFormRecord } from "./use-form-record.js";

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
  const { record, isLoading, isError, error, refetch, isDirty, setField, save, isSaving, saveError } = useFormRecord(
    view.resource,
    recordId,
    {
      autoSave: view.autosave ?? false,
      // A save may write a change entry to the chatter's feed, which
      // otherwise only refetches after the viewer's own comment or delete.
      onSaved: () => {
        if (recordId !== undefined) {
          void queryClient.invalidateQueries({ queryKey: recordActivityQueryKey(view.resource, recordId) });
        }
      },
      ...testFormRecordOptions,
    },
  );

  const conditions = useConditionEvaluator(`form "${view.name}"`);

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

  const formReadonly = conditions.isReadonly(view.readonly_condition, "readonly_condition", record);

  return (
    <PageLayout>
      {/* A defined Fragment even when both actions render null —
          PageHeader's empty-actions skip won't fire, but the resulting
          empty wrapper is harmless. */}
      <PageHeader
        title={view.label}
        actions={
          <>
            {view.workflow_actions && <WorkflowActions resource={view.resource} recordId={recordId} record={record} />}
            <ListActions actions={view.header_actions ?? []} module={module} record={record} viewName={view.name} />
            <ShareHeaderAction resource={view.resource} recordId={recordId} />
          </>
        }
      />

      {/* Not TwoColumnLayout — FormSidebar.width is per-view configurable,
          incompatible with its fixed 2/3+1/3 ratio; gap-8 (2rem) kept in sync with it. */}
      <div className="flex gap-8">
        <div className="flex-1 space-y-4">
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
                formReadonly={formReadonly}
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
            formReadonly={formReadonly}
          />

          {view.chatter !== false && <FormChatter view={view} recordId={recordId} />}
        </div>

        {view.sidebar && <FormSidebarRenderer sidebar={view.sidebar} record={record} />}
      </div>

      {!view.autosave && (
        // "Structure stays put" the way AlertDialog's own fixed button row
        // does, at a smaller scale — sticky, not scrolled away with a long
        // field list.
        <footer className="sticky bottom-0 flex items-center gap-4 border-t border-border bg-bg p-4">
          <ActionButton variant="primary" loading={isSaving} disabled={!isDirty || formReadonly} onClick={save}>
            Save
          </ActionButton>
          {saveError && (
            <span role="alert" className="text-sm text-danger">
              {saveError.message}
            </span>
          )}
        </footer>
      )}
      {view.autosave && isSaving && (
        <span role="status" className="text-sm text-text-secondary">
          Saving…
        </span>
      )}
      {view.autosave && saveError && (
        <span role="alert" className="text-sm text-danger">
          {saveError.message}
        </span>
      )}
    </PageLayout>
  );
}
