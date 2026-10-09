import { ActionButton, Badge, Button, EmptyState, Icon, PageHeader, PageLayout, Skeleton } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { type ConnectorSummary, providerCategoryLabel, useConnectors } from "./admin-connectors-api.js";

function initialOf(name: string): string {
  return name.trim().charAt(0).toLocaleUpperCase() || "?";
}

function ConnectorCard({ connector, onOpen }: { connector: ConnectorSummary; onOpen: () => void }): ReactNode {
  const multiple = connector.providers.length > 1;
  return (
    <li className="flex flex-col gap-4 rounded-structural border border-border bg-surface p-4">
      <div className="flex items-start gap-3">
        <span
          aria-hidden="true"
          className="flex size-10 shrink-0 items-center justify-center rounded-control bg-bg-subtle font-semibold text-text"
        >
          {initialOf(connector.displayName)}
        </span>
        <div className="flex min-w-0 flex-col gap-1">
          <h2 className="truncate font-semibold text-text">{connector.displayName}</h2>
          <p className="text-sm text-text-secondary">Version {connector.version}</p>
        </div>
      </div>
      {connector.description && <p className="text-sm text-text-secondary">{connector.description}</p>}
      <div className="flex flex-wrap items-center gap-2">
        <Badge
          label={connector.configured ? "Configured" : "Not configured"}
          color={connector.configured ? "green" : "gray"}
        />
        {connector.providers
          .filter((provider) => provider.canSetPrimary && provider.primary)
          .map((provider) => (
            <Badge
              key={provider.category}
              label={multiple ? `Primary ${providerCategoryLabel(provider.category)} provider` : "Primary"}
              color="blue"
              icon="check"
            />
          ))}
        {!connector.enabled && <Badge label="Disabled" color="orange" />}
      </div>
      <div className="mt-auto flex justify-end">
        <Button variant="secondary" onClick={onOpen} aria-label={`Configure ${connector.displayName}`}>
          Configure
        </Button>
      </div>
    </li>
  );
}

export interface ConnectorsPageProps {
  onOpenConnector: (name: string) => void;
}

export function ConnectorsPage({ onOpenConnector }: ConnectorsPageProps): ReactNode {
  const query = useConnectors();

  return (
    <PageLayout>
      <PageHeader
        title="Connectors"
        subtitle="Connect your organisation to payment, messaging and other outside services."
      />
      {query.isError ? (
        <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-text">Couldn't load connectors.</p>
          <ActionButton variant="secondary" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      ) : !query.data ? (
        <Skeleton type="card" />
      ) : query.data.length === 0 ? (
        <EmptyState
          icon="plug"
          title="No connectors installed"
          description="Install a connector module to connect an outside service."
        />
      ) : (
        <ul className="grid grid-cols-[repeat(auto-fill,minmax(16rem,1fr))] gap-4">
          {query.data.map((connector) => (
            <ConnectorCard key={connector.name} connector={connector} onOpen={() => onOpenConnector(connector.name)} />
          ))}
        </ul>
      )}
    </PageLayout>
  );
}
