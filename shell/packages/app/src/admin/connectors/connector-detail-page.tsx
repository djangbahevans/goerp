import {
  ActionButton,
  AlertDialog,
  Badge,
  Button,
  EmptyState,
  Icon,
  PageHeader,
  PageLayout,
  SectionCard,
  Skeleton,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useState } from "react";
import {
  type ConfigEntry,
  type ConnectorDetail,
  type ConnectorStatus,
  useConnector,
  useRotateConfigValue,
  useSaveConnectorConfig,
  useSetPrimaryConnector,
  useTestConnector,
} from "./admin-connectors-api.js";
import { ConfigField } from "./config-field.js";
import { type Draft, draftOf, groupByCategory, parseDraft, sameDraft, widgetOf } from "./config-widgets.js";

type Drafts = Record<string, Draft>;

function initialDrafts(entries: ConfigEntry[]): Drafts {
  return Object.fromEntries(entries.map((entry) => [entry.key, draftOf(entry)]));
}

// The server rejects a save with a 422 whose details map each key to its
// message.
function fieldErrorsOf(err: unknown, module: string): Record<string, string> | null {
  if (!(err instanceof AppError) || err.httpStatus !== 422 || !err.details) return null;
  const errors: Record<string, string> = {};
  for (const [qualified, message] of Object.entries(err.details)) {
    if (typeof message === "string")
      errors[qualified.startsWith(`${module}.`) ? qualified.slice(module.length + 1) : qualified] = message;
  }
  return Object.keys(errors).length > 0 ? errors : null;
}

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
  const initial = initialDrafts(connector.config);
  const [drafts, setDrafts] = useState<Drafts>(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [rotating, setRotating] = useState<ConfigEntry | null>(null);
  const save = useSaveConnectorConfig(connector.name);
  const rotate = useRotateConfigValue(connector.name);
  const test = useTestConnector(connector.name);

  const editable = connector.config.filter((entry) => widgetOf(entry) !== "generated");
  const changed = editable.filter((entry) => !sameDraft(drafts[entry.key] as Draft, initial[entry.key] as Draft));

  const change = (key: string, draft: Draft) => {
    setDrafts((current) => ({ ...current, [key]: draft }));
    setErrors(({ [key]: _, ...rest }) => rest);
  };

  const submit = () => {
    const nextErrors: Record<string, string> = {};
    const changes: Record<string, unknown> = {};
    for (const entry of changed) {
      const parsed = parseDraft(entry, drafts[entry.key] as Draft);
      if (parsed.ok) changes[entry.key] = parsed.value;
      else nextErrors[entry.key] = parsed.message;
    }
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    save.mutate(changes, {
      onSuccess: (result) => {
        onSaved();
        toast.success(
          result.restartRequired.length > 0
            ? "Configuration saved. Some changes take effect after the module reloads."
            : "Configuration saved.",
        );
      },
      onError: (err) => {
        const fieldErrors = fieldErrorsOf(err, connector.name);
        if (fieldErrors) setErrors(fieldErrors);
        else toast.error(err instanceof AppError && err.message ? err.message : "Couldn't save. Try again.");
      },
    });
  };

  const confirmRotate = () => {
    const entry = rotating;
    setRotating(null);
    if (!entry) return;
    rotate.mutate(entry.key, {
      onSuccess: () => toast.success(`${entry.label} rotated.`),
      onError: () => toast.error(`Couldn't rotate ${entry.label}.`),
    });
  };

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      {groupByCategory(connector.config).map(({ category, entries }) => (
        <SectionCard key={category} title={category}>
          <div className="mt-4 flex max-w-xl flex-col gap-5">
            {entries.map((entry) => (
              <ConfigField
                key={entry.key}
                entry={entry}
                draft={drafts[entry.key] as Draft}
                error={errors[entry.key]}
                onChange={(draft) => change(entry.key, draft)}
                onRotate={() => setRotating(entry)}
                rotating={rotate.isPending && rotate.variables === entry.key}
              />
            ))}
          </div>
        </SectionCard>
      ))}
      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" variant="primary" disabled={changed.length === 0 || save.isPending}>
          {save.isPending ? "Saving…" : "Save configuration"}
        </Button>
        {connector.hasStatusRoute && (
          <ActionButton variant="secondary" onClick={() => test.mutate()} loading={test.isPending}>
            Test connection
          </ActionButton>
        )}
      </div>
      <TestResult test={test} />
      <AlertDialog
        open={rotating !== null}
        title={`Rotate ${rotating?.label ?? "value"}?`}
        description="A new value is generated and the current one stops working."
        tone="warning"
        confirmLabel="Rotate"
        onCancel={() => setRotating(null)}
        onConfirm={confirmRotate}
      />
    </form>
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
    </PageLayout>
  );
}
