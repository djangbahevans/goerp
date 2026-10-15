import { PermissionContext, type PermissionContextValue, usePermissionsStatus } from "@goerp/sdk/auth";
import {
  AlertDialog,
  Icon,
  IconButton,
  MODAL_CONTENT_CLASSES,
  MODAL_OVERLAY_CLASSES,
  Skeleton,
  useReturnFocus,
} from "@goerp/sdk/components";
import { CurrentViewProvider } from "@goerp/sdk/nav";
import { toast } from "@goerp/sdk/notifications";
import { lastInstructionNavigation, type ModalEntry, modalManager } from "@goerp/sdk/realtime";
import {
  CREATE_PATH_SUFFIX,
  parseManifestValue,
  type ResolvedView,
  useViewRegistryStatus,
  type ViewRegistry,
  ViewRegistryContext,
} from "@goerp/sdk/schema";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { useQuery } from "@tanstack/react-query";
import { useRouterState } from "@tanstack/react-router";
import { type ReactNode, useContext, useEffect, useMemo, useRef, useState } from "react";
import { ensureModuleRegistered } from "../bootstrap/module-loader.js";
import { FormRenderer } from "../renderers/form/form-renderer.js";
import { FormViewDeclarationSchema } from "../renderers/form/form-view-types.js";
import { UnsavedChangesContext } from "../renderers/form/unsaved-changes.js";
import { DeferViewWhileUnsaved } from "../router/defer-view-while-unsaved.js";

type FormEntry = Extract<ModalEntry, { kind: "form" }>;

type Resolution =
  | { status: "loading" }
  | { status: "ready"; view: ResolvedView }
  | { status: "error"; message: string };

// The resource's default form view, through the route the page would open it
// by, so that route's permissions and capability filtering apply.
export function resolveFormModalView(
  registry: ViewRegistry,
  resource: string,
  recordId: string | undefined,
): ResolvedView | null {
  const metadata = registry.resourceMetadata.get(resource);
  if (!metadata?.defaultFormView) return null;
  if (recordId !== undefined) {
    const path = registry.recordViewPath(metadata.defaultFormView, metadata.module);
    return (path && registry.routes.get(path)) || null;
  }
  for (const [path, view] of registry.routes) {
    if (
      path.endsWith(CREATE_PATH_SUFFIX) &&
      view.module === metadata.module &&
      view.viewName === metadata.defaultFormView
    ) {
      return view;
    }
  }
  return null;
}

function resolve(
  registry: ViewRegistry,
  permissions: PermissionContextValue,
  resource: string,
  recordId: string | undefined,
): Resolution {
  const view = resolveFormModalView(registry, resource, recordId);
  if (!view) {
    return {
      status: "error",
      message: `There's no form to ${recordId === undefined ? "create" : "open"} this record.`,
    };
  }
  if (!permissions.moduleEnabled(view.module) || !view.permissions.every((p) => permissions.check(p))) {
    return { status: "error", message: "You don't have access to this record." };
  }
  return { status: "ready", view };
}

function useResolution(resource: string, recordId: string | undefined): Resolution {
  const registry = useContext(ViewRegistryContext);
  const permissions = useContext(PermissionContext);
  const registryStatus = useViewRegistryStatus();
  const permissionsStatus = usePermissionsStatus();
  return useMemo(() => {
    if (registryStatus === "error" || permissionsStatus === "error") {
      return { status: "error", message: "Couldn't load this form. Reload the page to try again." };
    }
    if (registryStatus !== "ready" || permissionsStatus !== "ready" || !registry || !permissions) {
      return { status: "loading" };
    }
    return resolve(registry, permissions, resource, recordId);
  }, [registry, permissions, registryStatus, permissionsStatus, resource, recordId]);
}

function ModalAlert({ children }: { children: ReactNode }): ReactNode {
  return (
    <div role="alert" className="flex flex-col items-center gap-2 px-6 py-10 text-center">
      <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
      <p className="text-text">{children}</p>
    </div>
  );
}

function FormSkeleton(): ReactNode {
  return (
    <div className="p-6">
      <Skeleton />
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
  if (bundle.isPending) return <FormSkeleton />;
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

// The open_form instruction's record form over the current page. A user's
// navigation closes it; a `navigate` instruction's doesn't.
export function FormModal({ entry }: { entry: FormEntry }): ReactNode {
  const { resource } = entry.config;
  const [recordId, setRecordId] = useState(entry.config.recordId);
  const resolution = useResolution(resource, recordId);
  const registry = useContext(ViewRegistryContext);
  const permissions = useContext(PermissionContext);
  const [unsaved, setUnsaved] = useState(false);
  const [confirmingClose, setConfirmingClose] = useState(false);
  const restoreFocus = useReturnFocus(true);
  const close = () => modalManager.close(entry.id);
  const requestClose = () => (unsaved ? setConfirmingClose(true) : close());
  const onCreated = (id: string) => {
    if (registry && permissions && resolve(registry, permissions, resource, id).status === "ready") {
      setRecordId(id);
      return;
    }
    toast.success("Saved.");
    close();
  };

  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const openedAt = useRef({ pathname, navigation: lastInstructionNavigation().count });
  useEffect(() => {
    if (pathname === openedAt.current.pathname) return;
    const navigation = lastInstructionNavigation();
    if (navigation.count !== openedAt.current.navigation && navigation.pathname === pathname) {
      openedAt.current = { pathname, navigation: navigation.count };
    } else {
      modalManager.close(entry.id);
    }
  }, [pathname, entry.id]);

  return (
    <DialogPrimitive.Root open>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className={MODAL_CONTENT_CLASSES}
          onCloseAutoFocus={restoreFocus}
          // Prevented so the form's page-level Escape (cancel the edit) stays out of it.
          onEscapeKeyDown={(event) => {
            event.preventDefault();
            requestClose();
          }}
          onInteractOutside={(event) => event.preventDefault()}
        >
          <div className="flex max-h-[90vh] w-full max-w-5xl flex-col overflow-hidden rounded-structural bg-bg shadow-lg">
            <DialogPrimitive.Title className="sr-only">
              {resolution.status === "ready" ? resolution.view.declaration.label : "Record"}
            </DialogPrimitive.Title>
            <div className="flex justify-end px-3 pt-3">
              <IconButton icon="x" label="Close" size="sm" onClick={requestClose} />
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              <UnsavedChangesContext value={setUnsaved}>
                {resolution.status === "ready" ? (
                  <DeferViewWhileUnsaved view={resolution.view}>
                    {(view) => <ResolvedForm view={view} recordId={recordId} onCreated={onCreated} />}
                  </DeferViewWhileUnsaved>
                ) : resolution.status === "loading" ? (
                  <FormSkeleton />
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
