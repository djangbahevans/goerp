import { ActionButton, isKnownIconName, SegmentedField, Skeleton } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { NotificationItem } from "../../chrome/notification-sheet.js";
import { RouterTextLink } from "../../router/text-link.js";
import {
  type TemplateChannel,
  type TemplateColumn,
  type TemplateFields,
  type TemplatePreview,
  type TemplateTarget,
  useNotificationTemplatePreview,
} from "./notification-templates-api.js";
import { COLUMN_LABELS } from "./template-rows.js";

export type PreviewResult =
  | { kind: "empty" }
  | { kind: "rendered"; preview: TemplatePreview }
  | { kind: "template_error"; field: TemplateColumn; message: string }
  | { kind: "setting_error" }
  | { kind: "failed" };

const DEBOUNCE_MS = 400;

function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  const key = JSON.stringify(value);
  // biome-ignore lint/correctness/useExhaustiveDependencies: keyed on the serialized value, so a new object with the same content doesn't restart the delay.
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [key, delayMs]);
  return debounced;
}

function hasContent(template: TemplateFields): boolean {
  return Object.values(template).some((source) => source !== undefined && source !== "");
}

function resultOf(error: unknown): PreviewResult {
  if (error instanceof AppError && error.code === "invalid_template") {
    const details = error.details ?? {};
    return {
      kind: "template_error",
      field: details.field as TemplateColumn,
      message: typeof details.message === "string" ? details.message : error.message,
    };
  }
  if (error instanceof AppError && error.code === "invalid_setting") return { kind: "setting_error" };
  return { kind: "failed" };
}

export interface NotificationPreview {
  // The draft the shown render was requested for.
  template: TemplateFields;
  // The newest settled outcome; null while the first request runs.
  result: PreviewResult | null;
  // The last successful render, kept while a newer request runs.
  rendered: TemplatePreview | null;
  renderedAt: number;
  retry: () => void;
}

// Renders the draft through the engine 400ms after its last change. The
// sheet runs it whichever side tab is open, since the SMS counter and the
// fields' template errors follow it too.
export function useNotificationPreview(
  target: TemplateTarget,
  template: TemplateFields,
  sampleData: Record<string, unknown>,
): NotificationPreview {
  const request = useDebounced({ template, sampleData }, DEBOUNCE_MS);
  const enabled = hasContent(request.template);
  const query = useNotificationTemplatePreview(target, request.template, request.sampleData, enabled);
  const settled = !query.isFetching && !query.isPlaceholderData;
  const result: PreviewResult | null = !enabled
    ? { kind: "empty" }
    : query.isError
      ? resultOf(query.error)
      : settled && query.data
        ? { kind: "rendered", preview: query.data }
        : null;
  const lastRender = useRef<{ template: TemplateFields; preview: TemplatePreview; at: number } | null>(null);
  if (query.data && !query.isPlaceholderData) {
    lastRender.current = { template: request.template, preview: query.data, at: query.dataUpdatedAt };
  }
  return {
    template: lastRender.current?.template ?? request.template,
    result,
    rendered: lastRender.current?.preview ?? null,
    renderedAt: lastRender.current?.at ?? 0,
    retry: () => void query.refetch(),
  };
}

export interface NotificationPreviewPanelProps {
  preview: NotificationPreview;
  type: string;
  // Shown as the in-app title when the draft's renders empty, as a send does.
  typeLabel: string;
  channel: TemplateChannel;
}

const MUTED = "text-sm text-text-secondary";

export function NotificationPreviewPanel({
  preview,
  type,
  typeLabel,
  channel,
}: NotificationPreviewPanelProps): ReactNode {
  const { result } = preview;
  if (result?.kind === "empty") return <p className={MUTED}>Add some content to see a preview.</p>;
  if (result?.kind === "template_error") {
    return (
      <div role="status" className="flex flex-col gap-1">
        <p className="text-sm text-text">Fix {COLUMN_LABELS[result.field] ?? result.field} to update the preview.</p>
        <p className="font-mono text-text-secondary text-xs">{result.message}</p>
      </div>
    );
  }
  if (result?.kind === "setting_error") {
    return (
      <p role="status" className="text-sm text-text">
        Your email layout can't be rendered, so this preview can't be shown. Check Email in{" "}
        <RouterTextLink to="/admin/settings" inline>
          Tenant settings
        </RouterTextLink>
        .
      </p>
    );
  }
  if (result?.kind === "failed") {
    return (
      <div role="status" className="flex flex-col items-start gap-2">
        <p className="text-sm text-text">Couldn't render the preview.</p>
        <ActionButton variant="secondary" size="sm" onClick={preview.retry}>
          Retry
        </ActionButton>
      </div>
    );
  }
  if (!preview.rendered) return <Skeleton lines={channel === "email" ? 8 : 3} />;

  const { rendered } = preview.rendered;
  switch (channel) {
    case "in_app":
      return (
        <InAppPreview
          type={type}
          typeLabel={typeLabel}
          rendered={rendered}
          renderedAt={new Date(preview.renderedAt).toISOString()}
        />
      );
    case "email":
      return <EmailPreview template={preview.template} rendered={rendered} />;
    case "sms":
      return (
        <p className="whitespace-pre-wrap rounded-control bg-bg-subtle px-3 py-2 text-sm text-text">
          {rendered.sms_template}
        </p>
      );
    case "push":
      return (
        <div className="flex flex-col gap-1 rounded-structural border border-border p-3 text-sm">
          {rendered.push_title_template ? (
            <p className="font-semibold text-text">{rendered.push_title_template}</p>
          ) : (
            <p className="text-text-secondary">Uses the in-app title</p>
          )}
          {rendered.push_body_template ? (
            <p className="text-text">{rendered.push_body_template}</p>
          ) : (
            <p className="text-text-secondary">Uses the in-app body</p>
          )}
        </div>
      );
  }
}

function InAppPreview({
  type,
  typeLabel,
  rendered,
  renderedAt,
}: {
  type: string;
  typeLabel: string;
  rendered: TemplateFields;
  renderedAt: string;
}): ReactNode {
  const icon = rendered.icon || null;
  return (
    <div className="flex flex-col gap-2">
      <div className="overflow-hidden rounded-structural border border-border">
        <NotificationItem
          notification={{
            id: "preview",
            type,
            module: "",
            title: rendered.title_template || typeLabel,
            body: rendered.body_template || null,
            actionUrl: null,
            icon,
            readAt: null,
            createdAt: renderedAt,
          }}
        />
      </div>
      {icon !== null && !isKnownIconName(icon) && (
        <p className="text-text-secondary text-xs">Icon {icon} isn't a known icon.</p>
      )}
      {rendered.action_url_template && (
        <p className="break-all text-text-secondary text-xs">{rendered.action_url_template}</p>
      )}
    </div>
  );
}

function EmailPreview({ template, rendered }: { template: TemplateFields; rendered: TemplateFields }): ReactNode {
  const [view, setView] = useState("html");
  const fromInApp = <p className={MUTED}>Built from the in-app title, body and link</p>;
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-text">
        Subject:{" "}
        {template.subject_template ? (
          rendered.subject_template
        ) : (
          <span className="text-text-secondary">Uses the in-app title</span>
        )}
      </p>
      <SegmentedField
        size="sm"
        options={[
          { value: "html", label: "HTML" },
          { value: "text", label: "Plain text" },
        ]}
        value={view}
        onChange={setView}
      />
      {view === "html" ? (
        template.html_template ? (
          <iframe
            title="Email preview"
            sandbox=""
            srcDoc={rendered.html_template}
            // Email clients render on white whatever the shell's theme, and a
            // layout that sets no background would otherwise show the dark surface.
            className="h-120 w-full rounded-structural border border-border bg-white"
          />
        ) : (
          fromInApp
        )
      ) : template.text_template ? (
        <pre className="whitespace-pre-wrap font-mono text-text text-xs">{rendered.text_template}</pre>
      ) : (
        fromInApp
      )}
    </div>
  );
}
