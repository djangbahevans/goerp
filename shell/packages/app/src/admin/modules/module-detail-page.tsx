import {
  ActionButton,
  AlertDialog,
  Button,
  EmptyState,
  PageHeader,
  PageLayout,
  SectionCard,
  Skeleton,
} from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useState } from "react";
import { conflictingModules, type ModuleSummary, useModules, useSetModuleEnabled } from "./admin-modules-api.js";
import { ModuleStatusBadge } from "./module-status.js";

function namesOf(names: string[], modules: ModuleSummary[]): string {
  const labels = names.map((name) => modules.find((m) => m.name === name)?.displayName ?? name);
  return labels.length > 1 ? `${labels.slice(0, -1).join(", ")} and ${labels.at(-1)}` : (labels[0] ?? "");
}

function StatusControl({ module, modules }: { module: ModuleSummary; modules: ModuleSummary[] }): ReactNode {
  const setEnabled = useSetModuleEnabled();
  const [confirming, setConfirming] = useState(false);

  if (!module.entitled) {
    return <p className="text-sm text-text-secondary">Your plan doesn't include {module.displayName}.</p>;
  }

  const change = (enabled: boolean) =>
    setEnabled.mutate(
      { name: module.name, enabled },
      {
        onSuccess: () => toast.success(`${module.displayName} ${enabled ? "enabled" : "disabled"}.`),
        onError: (err) => {
          const blocking = namesOf(conflictingModules(err), modules);
          toast.error(
            blocking
              ? enabled
                ? `Enable ${blocking} first.`
                : `Disable ${blocking} first.`
              : `Couldn't ${enabled ? "enable" : "disable"} ${module.displayName}.`,
          );
        },
      },
    );

  return (
    <>
      {module.enabled ? (
        <div className="flex flex-col items-start gap-2">
          <p className="text-sm text-text-secondary">Everyone in your organisation can use this module.</p>
          <ActionButton variant="danger" loading={setEnabled.isPending} onClick={() => setConfirming(true)}>
            Disable module
          </ActionButton>
        </div>
      ) : (
        <div className="flex flex-col items-start gap-2">
          <p className="text-sm text-text-secondary">This module is hidden from everyone in your organisation.</p>
          <ActionButton variant="primary" loading={setEnabled.isPending} onClick={() => change(true)}>
            Enable module
          </ActionButton>
        </div>
      )}
      <AlertDialog
        open={confirming}
        title={`Disable ${module.displayName}?`}
        description={`This will hide all ${module.displayName} data from users. Data is not deleted.`}
        tone="warning"
        confirmLabel="Disable"
        confirmVariant="danger"
        onCancel={() => setConfirming(false)}
        onConfirm={() => {
          setConfirming(false);
          change(false);
        }}
      />
    </>
  );
}

export interface ModuleDetailPageProps {
  name: string;
  onBackToList: () => void;
  onOpenModule?: ((name: string) => void) | undefined;
}

// shell-ux.md §5.3 "Module detail page".
export function ModuleDetailPage({ name, onBackToList, onOpenModule }: ModuleDetailPageProps): ReactNode {
  const query = useModules();
  const modules = query.data;
  const module = modules?.find((m) => m.name === name);

  if (query.isError) {
    return (
      <PageLayout>
        <EmptyState
          icon="boxes"
          title="Couldn't load this module"
          action={
            <ActionButton variant="secondary" onClick={() => void query.refetch()}>
              Retry
            </ActionButton>
          }
        />
      </PageLayout>
    );
  }
  if (!modules) {
    return (
      <PageLayout>
        <Skeleton type="card" />
      </PageLayout>
    );
  }
  if (!module) {
    return (
      <PageLayout>
        <EmptyState
          icon="boxes"
          title="Module not found"
          description="It isn't installed."
          action={
            <Button variant="secondary" onClick={onBackToList}>
              Back to modules
            </Button>
          }
        />
      </PageLayout>
    );
  }

  return (
    <PageLayout>
      <PageHeader
        title={module.displayName}
        subtitle={module.description || `Version ${module.version}`}
        actions={<ModuleStatusBadge module={module} />}
      />
      <div className="-mt-3 mb-6">
        <Button variant="link" size="sm" icon="arrow-left" onClick={onBackToList}>
          All modules
        </Button>
      </div>
      <div className="flex flex-col gap-6">
        <SectionCard title="Availability">
          <div className="mt-4">
            <StatusControl module={module} modules={modules} />
          </div>
        </SectionCard>
        <SectionCard title="Dependencies">
          {module.dependsOn.length === 0 ? (
            <p className="mt-4 text-sm text-text-secondary">This module doesn't depend on any other module.</p>
          ) : (
            <ul className="mt-4 flex flex-wrap gap-2">
              {module.dependsOn.map((dep) => {
                const label = modules.find((m) => m.name === dep)?.displayName ?? dep;
                return (
                  <li key={dep}>
                    {onOpenModule ? (
                      <Button variant="secondary" size="sm" onClick={() => onOpenModule(dep)}>
                        {label}
                      </Button>
                    ) : (
                      <span className="text-sm text-text">{label}</span>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </SectionCard>
        <SectionCard title="Permissions">
          {module.permissions.length === 0 ? (
            <p className="mt-4 text-sm text-text-secondary">This module declares no permissions.</p>
          ) : (
            <dl className="mt-4 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm">
              {module.permissions.map((permission) => (
                <div key={permission.name} className="contents">
                  <dt className="font-mono text-text">{permission.name}</dt>
                  <dd className="text-text-secondary">{permission.description}</dd>
                </div>
              ))}
            </dl>
          )}
        </SectionCard>
      </div>
    </PageLayout>
  );
}
