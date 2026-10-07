import { ActionButton, Badge, Button, EmptyState, Icon, PageHeader, PageLayout, Skeleton } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useState } from "react";
import { ConfigForm } from "../config/config-form.js";
import {
  type ConnectorDetail,
  type ConnectorStatus,
  useConnector,
  useRotateConfigValue,
  useSaveConnectorConfig,
  useSetPrimaryConnector,
  useTestConnector,
} from "./admin-connectors-api.js";
import { WebhookEndpointCard } from "./webhook-endpoint-card.js";

function StatusResult({ status }: { status: ConnectorStatus }): ReactNode {
  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
      {Object.entries(status).map(([key, value]) => (
        <div key={key} className="contents">
          <dt className="text-text-secondary">{key.replaceAll("_", " ")}</dt>
          <dd className="text-text">{typeof value === "object" ? JSON.stringify(value) : String(value)}</dd>
        </div>
      ))}
    </dl>
  );
}

function TestResult({ test }: { test: ReturnType<typeof useTestConnector> }): ReactNode {
  if (test.isPending) return <p className="text-sm text-text-secondary">Testing the connection…</p>;
  if (test.isError) {
    const message =
      test.error instanceof AppError && test.error.message ? test.error.message : "The connection test failed.";
    return (
      <p role="status" aria-label="Connection test" className="flex items-center gap-2 text-danger text-sm">
        <Icon name="circle-alert" size={16} aria-hidden="true" />
        {message}
      </p>
    );
  }
  if (test.data) {
    return (
      <div
        role="status"
        aria-label="Connection test"
        className="rounded-structural border border-border bg-bg-subtle p-3"
      >
        <p className="mb-2 flex items-center gap-2 font-medium text-sm text-text">
          <Icon name="circle-check" size={16} aria-hidden="true" />
          Connection test result
        </p>
        <StatusResult status={test.data} />
      </div>
    );
  }
  return null;
}

function ConnectorForm({ connector, onSaved }: { connector: ConnectorDetail; onSaved: () => void }): ReactNode {
  const save = useSaveConnectorConfig(connector.name);
  const rotate = useRotateConfigValue(connector.name);
  const test = useTestConnector(connector.name);

  return (
    <ConfigForm
      moduleName={connector.name}
      entries={connector.config}
      save={save}
      rotate={rotate}
      onSaved={onSaved}
      actions={
        connector.hasStatusRoute && (
          <ActionButton variant="secondary" onClick={() => test.mutate()} loading={test.isPending}>
            Test connection
          </ActionButton>
        )
      }
      footer={<TestResult test={test} />}
    />
  );
}

function PrimaryControl({ connector }: { connector: ConnectorDetail }): ReactNode {
  const setPrimary = useSetPrimaryConnector();
  const { provider } = connector;
  if (!provider?.canSetPrimary) return null;
  if (provider.primary) return <Badge label="Primary" color="blue" icon="check" />;
  return (
    <ActionButton
      variant="secondary"
      loading={setPrimary.isPending}
      onClick={() =>
        setPrimary.mutate(connector.name, {
          onSuccess: () => toast.success(`${connector.displayName} is now the primary provider.`),
          onError: () => toast.error("Couldn't change the primary provider."),
        })
      }
    >
      Set as primary provider
    </ActionButton>
  );
}

export interface ConnectorDetailPageProps {
  name: string;
  onBackToList: () => void;
}

// shell-ux.md §5.4 "Connector detail". The form is keyed on the saved values,
// so it resets to them once a save refetches, even when the saved values
// look unchanged, as a newly typed secret does.
export function ConnectorDetailPage({ name, onBackToList }: ConnectorDetailPageProps): ReactNode {
  const query = useConnector(name);
  const connector = query.data;
  const [saves, setSaves] = useState(0);

  if (query.isError) {
    const missing = query.error instanceof AppError && query.error.httpStatus === 404;
    return (
      <PageLayout>
        <EmptyState
          icon="plug"
          title={missing ? "Connector not found" : "Couldn't load this connector"}
          description={missing ? "It isn't installed for this organisation." : undefined}
          action={
            missing ? (
              <Button variant="secondary" onClick={onBackToList}>
                Back to connectors
              </Button>
            ) : (
              <ActionButton variant="secondary" onClick={() => void query.refetch()}>
                Retry
              </ActionButton>
            )
          }
        />
      </PageLayout>
    );
  }
  if (!connector) {
    return (
      <PageLayout>
        <Skeleton type="card" />
      </PageLayout>
    );
  }

  return (
    <PageLayout>
      <PageHeader
        title={connector.displayName}
        subtitle={connector.description || `Version ${connector.version}`}
        actions={
          <>
            <Badge
              label={connector.configured ? "Configured" : "Not configured"}
              color={connector.configured ? "green" : "gray"}
            />
            <PrimaryControl connector={connector} />
          </>
        }
      />
      <div className="-mt-3 mb-6">
        <Button variant="link" size="sm" icon="arrow-left" onClick={onBackToList}>
          All connectors
        </Button>
      </div>
      {connector.config.length === 0 ? (
        <EmptyState icon="settings" title="Nothing to configure" description="This connector has no settings." />
      ) : (
        <ConnectorForm
          key={`${saves}:${JSON.stringify(connector.config.map((entry) => [entry.key, entry.value, entry.isSet]))}`}
          connector={connector}
          onSaved={() => setSaves((n) => n + 1)}
        />
      )}
      {connector.webhookPath && (
        <div className="mt-6">
          <WebhookEndpointCard name={connector.name} path={connector.webhookPath} />
        </div>
      )}
    </PageLayout>
  );
}
