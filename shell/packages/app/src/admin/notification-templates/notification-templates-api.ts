import { apiClient } from "@goerp/sdk";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

// The notification templates API (shell-ux.md §5.8 "Notification templates
// API"). Template content stays keyed by its notification_templates column
// names, the identifiers the routes themselves use.

export type TemplateChannel = "in_app" | "email" | "sms" | "push";

export type TemplateColumn =
  | "title_template"
  | "body_template"
  | "action_url_template"
  | "icon"
  | "subject_template"
  | "html_template"
  | "text_template"
  | "sms_template"
  | "push_title_template"
  | "push_body_template";

export type TemplateFields = Partial<Record<TemplateColumn, string>>;

export const CHANNEL_COLUMNS: Record<TemplateChannel, TemplateColumn[]> = {
  in_app: ["title_template", "body_template", "action_url_template", "icon"],
  email: ["subject_template", "html_template", "text_template"],
  sms: ["sms_template"],
  push: ["push_title_template", "push_body_template"],
};

export interface TemplateTarget {
  type: string;
  channel: TemplateChannel;
  locale: string;
}

export interface TemplateEntry {
  channel: TemplateChannel;
  locale: string;
  customised: boolean;
  hasDefault: boolean;
}

export interface TemplateType {
  type: string;
  module: string;
  label: string;
  availableChannels: TemplateChannel[];
  templates: TemplateEntry[];
}

export type VariableType = "string" | "int" | "float" | "bool";

export interface TemplateVariable {
  name: string;
  type: VariableType;
  source: "data" | "engine";
}

export interface NotificationTemplate extends TemplateTarget {
  default: TemplateFields | null;
  override: TemplateFields | null;
  variables: TemplateVariable[];
}

export interface TemplatePreview {
  rendered: TemplateFields;
  sms: { characters: number; segments: number } | null;
}

interface ListWire {
  types: {
    type: string;
    module: string;
    label: string;
    available_channels: TemplateChannel[];
    templates: { channel: TemplateChannel; locale: string; customised: boolean; has_default: boolean }[];
  }[];
}

interface PreviewWire {
  rendered: TemplateFields;
  sms?: { characters: number; segments: number };
}

function templatePath({ type, channel, locale }: TemplateTarget): string {
  return `/admin/settings/notification-templates/${encodeURIComponent(type)}/${channel}/${encodeURIComponent(locale)}`;
}

const notificationTemplatesKey = ["admin-notification-templates"] as const;

export const notificationTemplatesKeys = {
  all: notificationTemplatesKey,
  list: () => [...notificationTemplatesKey, "list"] as const,
  template: (target: TemplateTarget) =>
    [...notificationTemplatesKey, "template", target.type, target.channel, target.locale] as const,
  preview: (target: TemplateTarget, template: TemplateFields, data: Record<string, unknown>) =>
    [...notificationTemplatesKey, "preview", target.type, target.channel, target.locale, template, data] as const,
};

export function useNotificationTemplates() {
  return useQuery({
    queryKey: notificationTemplatesKeys.list(),
    queryFn: async ({ signal }): Promise<TemplateType[]> => {
      const wire = await apiClient.get<ListWire>("/admin/settings/notification-templates", { signal });
      return wire.types.map((t) => ({
        type: t.type,
        module: t.module,
        label: t.label,
        availableChannels: t.available_channels,
        templates: t.templates.map((e) => ({
          channel: e.channel,
          locale: e.locale,
          customised: e.customised,
          hasDefault: e.has_default,
        })),
      }));
    },
  });
}

export function useNotificationTemplate(target: TemplateTarget | null) {
  return useQuery({
    queryKey: target ? notificationTemplatesKeys.template(target) : [...notificationTemplatesKey, "template", null],
    enabled: target !== null,
    queryFn: async ({ signal }) =>
      apiClient.get<NotificationTemplate>(templatePath(target as TemplateTarget), { signal }),
  });
}

function useTemplateMutation<TInput extends { target: TemplateTarget }>(
  request: (input: TInput) => Promise<NotificationTemplate>,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: request,
    onSuccess: (template, { target }) => {
      queryClient.setQueryData(notificationTemplatesKeys.template(target), template);
      return queryClient.invalidateQueries({ queryKey: notificationTemplatesKeys.list() });
    },
  });
}

// Upserts the tenant's override. An empty column is dropped by the engine
// and stored as NULL.
export function useSaveNotificationTemplate() {
  return useTemplateMutation(({ target, fields }: { target: TemplateTarget; fields: TemplateFields }) =>
    apiClient.put<NotificationTemplate>(templatePath(target), fields),
  );
}

// Deletes the tenant's override, which removes the template outright when
// no default ships for it.
export function useResetNotificationTemplate() {
  return useTemplateMutation(({ target }: { target: TemplateTarget }) =>
    apiClient.delete<NotificationTemplate>(templatePath(target)),
  );
}

// Renders a draft against sample data. A newer key supersedes the in-flight
// request, which React Query aborts through `signal`; the last result stays
// shown meanwhile.
export function useNotificationTemplatePreview(
  target: TemplateTarget,
  template: TemplateFields,
  data: Record<string, unknown>,
  enabled: boolean,
) {
  return useQuery({
    queryKey: notificationTemplatesKeys.preview(target, template, data),
    enabled,
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
    placeholderData: keepPreviousData,
    queryFn: async ({ signal }): Promise<TemplatePreview> => {
      const wire = await apiClient.post<PreviewWire>(`${templatePath(target)}/preview`, { template, data }, { signal });
      return { rendered: wire.rendered, sms: wire.sms ?? null };
    },
  });
}
