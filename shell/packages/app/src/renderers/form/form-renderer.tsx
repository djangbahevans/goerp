import { ListActions } from "../list/list-actions.js";
import { FormSectionRenderer } from "./form-sections.js";
import { ShareHeaderAction } from "./form-share-action.js";
import { FormSidebarRenderer } from "./form-sidebar.js";
import { FormTabsRenderer } from "./form-tabs.js";
import type { FormViewDeclaration } from "./form-view-types.js";
import { useFormRecord } from "./use-form-record.js";

// shell-architecture.md §20's FormRenderer.
export interface FormRendererProps {
  view: FormViewDeclaration;
  module: string;
  recordId?: string;
}

export function FormRenderer({ view, module, recordId }: FormRendererProps) {
  const { record, isLoading, isError, error, refetch, isDirty, setField, save, isSaving, saveError } = useFormRecord(
    view.resource,
    recordId,
    { autoSave: view.autosave ?? false },
  );

  if (isLoading) {
    return (
      <div role="status" aria-label={`Loading ${view.label}`}>
        Loading…
      </div>
    );
  }

  if (isError) {
    return (
      <div role="alert">
        <p>Couldn't load {view.label}.</p>
        {error && <p>{error.message}</p>}
        <button type="button" onClick={refetch}>
          Retry
        </button>
      </div>
    );
  }

  // `readonly_condition` is typed but unevaluated — stays editable.
  const formReadonly = false;

  return (
    <div>
      <header>
        <h1>{view.label}</h1>
        <ListActions actions={view.header_actions ?? []} module={module} />
        <ShareHeaderAction resource={view.resource} recordId={recordId} />
      </header>

      <div style={{ display: "flex", gap: "2rem" }}>
        <div style={{ flex: 1 }}>
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

          {view.tabs && view.tabs.length > 0 && (
            <FormTabsRenderer
              tabs={view.tabs}
              resource={view.resource}
              module={module}
              record={record}
              recordId={recordId}
              onChange={setField}
              formReadonly={formReadonly}
            />
          )}

          {view.chatter !== false && (
            // Real chatter panel needs an activities API that doesn't exist yet (goerp#649).
            <section aria-label="Activity">
              <p>Activity feed not available yet.</p>
            </section>
          )}
        </div>

        {view.sidebar && <FormSidebarRenderer sidebar={view.sidebar} record={record} />}
      </div>

      {!view.autosave && (
        <footer>
          <button type="button" onClick={() => void save()} disabled={!isDirty || isSaving}>
            {isSaving ? "Saving…" : "Save"}
          </button>
          {saveError && <span role="alert">{saveError.message}</span>}
        </footer>
      )}
      {view.autosave && isSaving && <span role="status">Saving…</span>}
      {view.autosave && saveError && <span role="alert">{saveError.message}</span>}
    </div>
  );
}
