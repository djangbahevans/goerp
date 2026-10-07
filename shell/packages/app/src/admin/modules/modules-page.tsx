import {
  ActionButton,
  Button,
  EmptyState,
  Icon,
  PageHeader,
  PageLayout,
  Skeleton,
  TabPanel,
  Tabs,
} from "@goerp/sdk/components";
import { type ReactNode, useState } from "react";
import { type ModuleSummary, useModules } from "./admin-modules-api.js";
import { ModuleStatusBadge } from "./module-status.js";

type Filter = "all" | "active" | "disabled";

const FILTERS: { id: Filter; label: string; matches: (module: ModuleSummary) => boolean }[] = [
  { id: "all", label: "All", matches: () => true },
  { id: "active", label: "Active", matches: (m) => m.enabled },
  { id: "disabled", label: "Disabled", matches: (m) => !m.enabled },
];

function ModuleCard({ module, onOpen }: { module: ModuleSummary; onOpen: () => void }): ReactNode {
  return (
    <li className="flex flex-col gap-4 rounded-structural border border-border bg-surface p-4">
      <div className="flex items-start gap-3">
        <span
          aria-hidden="true"
          className="flex size-10 shrink-0 items-center justify-center rounded-control bg-bg-subtle font-semibold text-text"
        >
          {module.displayName.trim().charAt(0).toLocaleUpperCase() || "?"}
        </span>
        <div className="flex min-w-0 flex-col gap-1">
          <h2 className="truncate font-semibold text-text">{module.displayName}</h2>
          <p className="text-sm text-text-secondary">Version {module.version}</p>
        </div>
      </div>
      {module.description && <p className="text-sm text-text-secondary">{module.description}</p>}
      <div className="mt-auto flex items-center justify-between gap-2">
        <ModuleStatusBadge module={module} />
        <Button variant="secondary" onClick={onOpen} aria-label={`View ${module.displayName}`}>
          View
        </Button>
      </div>
    </li>
  );
}

export interface ModulesPageProps {
  onOpenModule: (name: string) => void;
}

// shell-ux.md §5.3 "Modules".
export function ModulesPage({ onOpenModule }: ModulesPageProps): ReactNode {
  const query = useModules();
  const [filter, setFilter] = useState<Filter>("all");

  const active = FILTERS.find((f) => f.id === filter) ?? FILTERS[0];
  const shown = (query.data ?? []).filter((m) => active?.matches(m));

  return (
    <PageLayout>
      <PageHeader title="Modules" subtitle="Choose which of your plan's modules your organisation uses." />
      {query.isError ? (
        <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-text">Couldn't load modules.</p>
          <ActionButton variant="secondary" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      ) : !query.data ? (
        <Skeleton type="card" />
      ) : query.data.length === 0 ? (
        <EmptyState
          icon="boxes"
          title="No modules installed"
          description="Modules appear here once they are installed."
        />
      ) : (
        <div className="flex flex-col gap-4">
          <Tabs
            items={FILTERS.map((f) => ({ id: f.id, label: f.label }))}
            activeId={filter}
            onChange={(id) => setFilter(id as Filter)}
          >
            <TabPanel id={filter}>
              {shown.length === 0 ? (
                <div className="pt-4">
                  <EmptyState icon="boxes" title={`No ${active?.label.toLowerCase()} modules`} />
                </div>
              ) : (
                <ul className="grid grid-cols-[repeat(auto-fill,minmax(16rem,1fr))] gap-4 pt-4">
                  {shown.map((module) => (
                    <ModuleCard key={module.name} module={module} onOpen={() => onOpenModule(module.name)} />
                  ))}
                </ul>
              )}
            </TabPanel>
          </Tabs>
        </div>
      )}
    </PageLayout>
  );
}
