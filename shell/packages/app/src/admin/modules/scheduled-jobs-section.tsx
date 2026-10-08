import { useAuth } from "@goerp/sdk/auth";
import { ActionButton, FieldWrapper, SectionCard, Skeleton, ToggleField } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react";
import { type ModuleSummary, moduleKeys } from "./admin-modules-api.js";
import { type CronJob, type CronJobs, type cronKeys, setCronEnabled, useCronJobs } from "./cron-jobs-api.js";

const blockedText = {
  module_not_entitled: "Your plan does not include this module.",
  module_not_ready: "This module is unavailable.",
  module_disabled: "Enable this module to run its scheduled jobs.",
  job_disabled: undefined,
};

function CronRow({
  job,
  label,
  module,
  queryKey,
  refresh,
  announce,
}: {
  job: CronJob;
  label: string;
  module: string;
  queryKey: ReturnType<typeof cronKeys.module>;
  refresh: () => Promise<boolean>;
  announce: (message: string) => void;
}): ReactNode {
  const client = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [error, setError] = useState<string>();
  const submitting = useRef(false);
  const active = useRef(true);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);

  const blocked = job.blocked_reason === "module_not_entitled" || job.blocked_reason === "module_not_ready";
  const help = job.blocked_reason ? blockedText[job.blocked_reason] : undefined;

  const reconcile = async () => {
    const refreshed = await refresh();
    if (active.current) setUncertain(!refreshed);
  };
  const change = async (enabled: boolean) => {
    if (submitting.current || uncertain || blocked) return;
    submitting.current = true;
    setBusy(true);
    setError(undefined);
    try {
      const saved = await setCronEnabled(module, job, enabled);
      if (!active.current) return;
      client.setQueryData<CronJobs>(
        queryKey,
        (current) =>
          current && {
            ...current,
            cron_jobs: current.cron_jobs.map((item) => (item.name === saved.name ? saved : item)),
          },
      );
      announce(`${job.label} ${saved.enabled ? "enabled" : "disabled"}`);
      await refresh();
    } catch (err) {
      if (!active.current) return;
      setError(
        err instanceof AppError && err.code === "cron_settings_conflict"
          ? "This job changed in another session. Check its current state and try again."
          : "Couldn't save scheduled job settings. Check the current state and try again.",
      );
      setUncertain(true);
      if (err instanceof AppError && err.httpStatus === 404) {
        await client.invalidateQueries({ queryKey: moduleKeys.all });
      }
      await reconcile();
    } finally {
      submitting.current = false;
      if (active.current) setBusy(false);
    }
  };

  return (
    <li
      data-cron-name={job.name}
      aria-busy={busy}
      className="flex min-w-0 flex-col gap-4 border-border border-b py-3 last:border-b-0 lg:flex-row lg:items-start lg:justify-between"
    >
      <div className="wrap-anywhere min-w-0 flex-1">
        <p className="font-medium text-base text-text">{job.label}</p>
        <p className="break-all font-mono text-sm text-text-secondary">{job.name}</p>
        {job.description && <p className="text-sm text-text-secondary">{job.description}</p>}
        <p className="text-sm text-text-secondary">
          <span className="font-mono">{job.schedule}</span> UTC
        </p>
        {job.next_run_at && (
          <p className="text-sm text-text-secondary">
            Next scheduled:{" "}
            {new Date(job.next_run_at)
              .toISOString()
              .replace("T", " ")
              .replace(/\.\d{3}Z$/, " UTC")}
          </p>
        )}
        <p className="text-sm text-text">{job.enabled ? "Enabled" : "Disabled"}</p>
      </div>
      <div
        aria-disabled={busy || undefined}
        className="wrap-anywhere min-h-12 min-w-0 shrink-0 lg:w-64 [&_label:not([for])]:before:absolute [&_label:not([for])]:before:-inset-x-1.5 [&_label:not([for])]:before:-inset-y-3.5 [&_label:not([for])]:before:min-h-12 [&_label:not([for])]:before:min-w-12 [&_label:not([for])]:before:content-[''] [&_label[for]]:inline-flex [&_label[for]]:min-h-12 [&_label[for]]:items-center"
      >
        <FieldWrapper label={`Enable ${label}`} description={help} error={error}>
          <ToggleField
            value={job.enabled}
            disabled={blocked || uncertain}
            onChange={(enabled) => {
              void change(enabled);
            }}
          />
        </FieldWrapper>
        {busy && <p className="mt-2 text-sm text-text-secondary">Saving…</p>}
        {uncertain && (
          <ActionButton variant="secondary" onClick={() => void reconcile()}>
            Retry
          </ActionButton>
        )}
      </div>
    </li>
  );
}

function TenantScheduledJobs({ module }: { module: ModuleSummary }): ReactNode {
  const { query, key } = useCronJobs(module.name);
  const client = useQueryClient();
  const heading = useRef<HTMLHeadingElement>(null);
  const focusedJob = useRef<string | null>(null);
  const [announcement, announce] = useState("");
  const removed = query.error instanceof AppError && query.error.httpStatus === 404;
  const jobs = removed ? [] : query.data?.cron_jobs;
  const lastAvailability = useRef(`${module.enabled}:${module.entitled}:${module.version}`);

  useEffect(() => {
    const availability = `${module.enabled}:${module.entitled}:${module.version}`;
    if (lastAvailability.current === availability) return;
    lastAvailability.current = availability;
    void query.refetch();
  }, [module.enabled, module.entitled, module.version, query.refetch]);

  useEffect(
    () => () => {
      void client.cancelQueries({ queryKey: key });
      client.removeQueries({ queryKey: key });
    },
    [client, key],
  );

  useLayoutEffect(() => {
    if (focusedJob.current && jobs && !jobs.some((job) => job.name === focusedJob.current)) {
      focusedJob.current = null;
      heading.current?.focus();
      announce("The scheduled job is unavailable.");
    }
  }, [jobs]);

  useEffect(() => {
    if (query.error instanceof AppError && query.error.httpStatus === 404) {
      void client.invalidateQueries({ queryKey: moduleKeys.all });
    }
  }, [client, query.error]);

  const refresh = async () => !(await query.refetch()).isError;

  return (
    <SectionCard title="Scheduled jobs" headingRef={heading}>
      <p className="text-sm text-text-secondary">
        Disabling skips queued runs and retries. Runs that have already started may finish.
      </p>
      <p role="status" aria-live="polite" className="sr-only">
        {announcement}
      </p>
      {query.isError && (
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <p className="text-danger text-sm">Couldn't load scheduled jobs</p>
          <ActionButton variant="secondary" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      )}
      {!jobs && !query.isError && <Skeleton type="card" />}
      {jobs?.length === 0 && <p className="mt-3 text-sm text-text-secondary">This module has no scheduled jobs.</p>}
      {jobs && (
        <ul
          className="mt-3"
          onFocusCapture={(event) => {
            focusedJob.current =
              (event.target as HTMLElement).closest<HTMLElement>("[data-cron-name]")?.dataset.cronName ?? null;
          }}
          onBlurCapture={(event) => {
            if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node))
              focusedJob.current = null;
          }}
        >
          {jobs.map((job) => (
            <CronRow
              key={job.name}
              job={job}
              label={
                jobs.filter((other) => other.label === job.label).length > 1 ? `${job.label} (${job.name})` : job.label
              }
              module={module.name}
              queryKey={key}
              refresh={refresh}
              announce={announce}
            />
          ))}
        </ul>
      )}
    </SectionCard>
  );
}

export function ScheduledJobsSection({ module }: { module: ModuleSummary }): ReactNode {
  const { tenant, user, isAuthenticated } = useAuth();
  if (!isAuthenticated || !tenant || !user?.roles.includes("admin")) return null;
  return <TenantScheduledJobs key={`${tenant.id}:${user.id}:${module.name}`} module={module} />;
}
