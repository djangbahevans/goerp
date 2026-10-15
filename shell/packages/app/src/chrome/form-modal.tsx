import { PermissionContext } from "@goerp/sdk/auth";
import {
  AlertDialog,
  Icon,
  IconButton,
  MODAL_CONTENT_CLASSES,
  MODAL_OVERLAY_CLASSES,
  Skeleton,
} from "@goerp/sdk/components";
import { CurrentViewProvider } from "@goerp/sdk/nav";
import { type ModalEntry, modalManager } from "@goerp/sdk/realtime";
import { parseManifestValue, type ResolvedView, type ViewRegistry, ViewRegistryContext } from "@goerp/sdk/schema";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { useQuery } from "@tanstack/react-query";
import { useRouterState } from "@tanstack/react-router";
import { type ReactNode, useContext, useEffect, useMemo, useRef, useState } from "react";
import { ensureModuleRegistered } from "../bootstrap/module-loader.js";
import { FormRenderer } from "../renderers/form/form-renderer.js";
import { FormViewDeclarationSchema } from "../renderers/form/form-view-types.js";
import { UnsavedChangesContext } from "../renderers/form/unsaved-changes.js";

type FormEntry = Extract<ModalEntry, { kind: "form" }>;

type Resolution = { ok: true; view: ResolvedView } | { ok: false; message: string };

// The resource's default form view, through the same route the page would
// open it by, so it carries that route's permissions and capability filtering.
export function resolveFormModalView(
  registry: ViewRegistry,
  resource: string,
  recordId: string | undefined,
): ResolvedView | null {
  const metadata = registry.resourceMetadata.get(resource);
  if (!metadata?.defaultFormView) return null;
  for (const [path, view] of registry.routes) {
    if (view.module !== metadata.module || view.viewName !== metadata.defaultFormView) continue;
    if (recordId === undefined ? path.endsWith("/new") : path.endsWith("/{id}")) return view;
  }
  return null;
}

function useResolution(resource: string, recordId: string | undefined): Resolution {
  const registry = useContext(ViewRegistryContext);
  const permissions = useContext(PermissionContext);
  const view = registry ? resolveFormModalView(registry, resource, recordId) : null;
  if (!view) {
    return { ok: false, message: `There's no form to ${recordId === undefined ? "create" : "open"} this record.` };
  }
  if (!permissions?.moduleEnabled(view.module) || !view.permissions.every((p) => permissions.check(p))) {
    return { ok: false, message: "You don't have access to this record." };
  }
  return { ok: true, view };
}

function ModalAlert({ children }: { children: ReactNode }): ReactNode {
  return (
    <div role="alert" className="flex flex-col items-center gap-2 px-6 py-10 text-center">
      <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
      <p className="text-text">{children}</p>
    </div>
  );
}

function ResolvedForm({
  view,
  recordId,
  onCreated,
}: {
  view: ResolvedView;
  recordId: string | undefined;
  onCreated: (id: string) => void;
}): ReactNode {
  const registry = useContext(ViewRegistryContext);
  const bundleSha256 = registry?.getBundleSHA256(view.module) ?? null;
  const bundle = useQuery({
    queryKey: ["module-bundle", view.module, view.bundleUrl, bundleSha256],
    queryFn: async () => {
      await ensureModuleRegistered(view.module, view.bundleUrl, bundleSha256);
      return true;
    },
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
  });
  const parsed = useMemo(() => parseManifestValue(FormViewDeclarationSchema, view.declaration), [view.declaration]);
  const currentView = useMemo(
    () => ({
      viewName: view.viewName,
      moduleName: view.module,
      resource: view.declaration.resource,
      recordId: recordId ?? null,
      embedded: false,
      params: new URLSearchParams(),
    }),
    [view, recordId],
  );
  const modal = useMemo(() => ({ onCreated }), [onCreated]);

  if (!parsed.success) return <ModalAlert>{`"${view.viewName}" isn't a valid form view.`}</ModalAlert>;
  if (bundle.isError) return <ModalAlert>{`Couldn't load the ${view.module} module.`}</ModalAlert>;
  if (bundle.isPending) {
    return (
      <div className="p-6">
        <Skeleton />
      </div>
    );
  }
  return (
    <CurrentViewProvider value={currentView}>
      <FormRenderer
        view={parsed.output}
        module={view.module}
        modal={modal}
        {...(recordId !== undefined ? { recordId } : {})}
      />
    </CurrentViewProvider>
  );
}

// The open_form instruction's record form, over the current page. Closing it
// by Escape or the close button asks first while the form has unsaved changes;
// a navigation of the page underneath closes it.
export function FormModal({ entry }: { entry: FormEntry }): ReactNode {
  const { resource } = entry.config;
  const [recordId, setRecordId] = useState(entry.config.recordId);
  const resolution = useResolution(resource, recordId);
  const [unsaved, setUnsaved] = useState(false);
  const [confirmingClose, setConfirmingClose] = useState(false);
  // Captured before Radix moves focus into the dialog; Radix restores focus
  // only to a Dialog.Trigger, which a server-opened modal has none of.
  const returnFocus = useRef<HTMLElement | null>(null);
  const close = () => modalManager.close(entry.id);
  const requestClose = () => (unsaved ? setConfirmingClose(true) : close());

  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const openedAt = useRef(pathname);
  useEffect(() => {
    if (pathname !== openedAt.current) modalManager.close(entry.id);
  }, [pathname, entry.id]);

  return (
    <DialogPrimitive.Root open>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className={MODAL_CONTENT_CLASSES}
          onOpenAutoFocus={() => {
            returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            returnFocus.current?.focus();
          }}
          // Prevented so the form's own Escape handling (cancelling an edit)
          // stays out of it; closing asks first when there are unsaved changes.
          onEscapeKeyDown={(event) => {
            event.preventDefault();
            requestClose();
          }}
          onInteractOutside={(event) => event.preventDefault()}
        >
          <div className="flex max-h-[90vh] w-full max-w-5xl flex-col overflow-hidden rounded-structural bg-bg shadow-lg">
            <DialogPrimitive.Title className="sr-only">
              {resolution.ok ? resolution.view.declaration.label : "Record"}
            </DialogPrimitive.Title>
            <div className="flex justify-end px-3 pt-3">
              <IconButton icon="x" label="Close" size="sm" onClick={requestClose} />
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              <UnsavedChangesContext value={setUnsaved}>
                {resolution.ok ? (
                  <ResolvedForm view={resolution.view} recordId={recordId} onCreated={setRecordId} />
                ) : (
                  <ModalAlert>{resolution.message}</ModalAlert>
                )}
              </UnsavedChangesContext>
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
      <AlertDialog
        open={confirmingClose}
        title="Leave without saving?"
        description="Your unsaved changes to this record will be lost."
        tone="warning"
        confirmLabel="Leave"
        cancelLabel="Stay"
        confirmVariant="danger"
        onCancel={() => setConfirmingClose(false)}
        onConfirm={close}
      />
    </DialogPrimitive.Root>
  );
}
