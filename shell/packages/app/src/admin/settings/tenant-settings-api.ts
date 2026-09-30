import { apiClient } from "@goerp/sdk";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

// The tenant settings API (shell-ux.md §5.5 "API" and "Notification
// delivery API"). The wire shapes are snake_case; the page works with
// these camelCase ones.

export type MFAMode = "optional" | "required" | "required_for_roles";
export type PasswordEnforcement = "nudge" | "require";
export type EmailVerificationPolicy = "required" | "tenant_choice" | "off";
export type FirstDayOfWeek = "monday" | "sunday";
export type EmailProvider = "resend" | "smtp";

export interface GeneralSettings {
  name: string;
  logoUrl: string | null;
  address: string;
  website: string;
  country: string;
  defaultCurrency: string;
  defaultLocale: string;
  defaultTimezone: string;
}

export interface SecuritySettings {
  mfa: { mode: MFAMode; requiredRoles: string[]; maxAssuranceAgeHours: number };
  passwordPolicy: { minLength: number; enforcement: PasswordEnforcement; graceDays: number };
  session: { idleTimeoutMinutes: number; absoluteMaxMinutes: number };
  ipAllowlist: string;
  emailVerification: { policy: EmailVerificationPolicy; required: boolean };
}

export interface LocalisationSettings {
  platformLocales: string[];
  availableLocales: string[];
  firstDayOfWeek: FirstDayOfWeek;
  numberFormat: string;
}

export interface TenantSettings {
  general: GeneralSettings;
  security: SecuritySettings;
  localisation: LocalisationSettings;
}

export interface TenantSettingsPatch {
  general?: Partial<Omit<GeneralSettings, "logoUrl">>;
  security?: {
    mfa?: Partial<SecuritySettings["mfa"]>;
    passwordPolicy?: Partial<SecuritySettings["passwordPolicy"]>;
    session?: Partial<SecuritySettings["session"]>;
    ipAllowlist?: string;
    emailVerification?: { required: boolean };
  };
  localisation?: Partial<Omit<LocalisationSettings, "platformLocales">>;
}

export interface SMTPSettings {
  host: string;
  port: number;
  user: string;
  // SECRET_MASK when one is stored, "" when not.
  password: string;
  useTLS: boolean;
}

export interface EmailSettings {
  provider: EmailProvider;
  // SECRET_MASK when one is stored, "" when not.
  apiKey: string;
  fromName: string;
  fromAddr: string;
  replyTo: string;
  layoutTemplate: string;
  smtp: SMTPSettings;
}

export interface NotificationTypeInfo {
  type: string;
  module: string;
  label: string;
  defaultChannels: string[];
  availableChannels: string[];
}

export interface NotificationDelivery {
  channels: { emailEnabled: boolean; smsEnabled: boolean; pushEnabled: boolean };
  email: EmailSettings;
  sms: { senderId: string };
  defaults: Record<string, string[]>;
  types: NotificationTypeInfo[];
  // Fields an operator override fixes, such as "email.provider".
  locked: string[];
}

export type EmailSettingsPatch = Partial<Omit<EmailSettings, "smtp">> & { smtp?: Partial<SMTPSettings> };

export interface NotificationDeliveryPatch {
  channels?: Partial<NotificationDelivery["channels"]>;
  email?: EmailSettingsPatch;
  sms?: { senderId: string };
  defaults?: Record<string, string[] | null>;
}

export interface TestEmailResult {
  sentTo: string;
  provider: string;
}

// What GET returns for a stored secret, and what a PATCH sends back to keep it.
export const SECRET_MASK = "***";

interface SettingsWire {
  general: {
    name: string;
    logo_url: string | null;
    address: string | null;
    website: string | null;
    country: string | null;
    default_currency: string | null;
    default_locale: string;
    default_timezone: string;
  };
  security: {
    mfa: { mode: MFAMode; required_roles: string[]; max_assurance_age_hours: number };
    password_policy: { min_length: number; enforcement: PasswordEnforcement; grace_days: number };
    session: { idle_timeout_minutes: number; absolute_max_minutes: number };
    ip_allowlist: string;
    email_verification: { policy: EmailVerificationPolicy; required: boolean };
  };
  localisation: {
    platform_locales: string[];
    available_locales: string[];
    first_day_of_week: FirstDayOfWeek;
    number_format: string;
  };
}

interface DeliveryWire {
  channels: { email_enabled: boolean; sms_enabled: boolean; push_enabled: boolean };
  email: {
    provider: EmailProvider;
    api_key: string;
    from_name: string;
    from_addr: string;
    reply_to: string;
    layout_template: string;
    smtp: { host: string; port: number; user: string; password: string; use_tls: boolean };
  };
  sms: { sender_id: string };
  defaults: Record<string, string[]>;
  types: {
    type: string;
    module: string;
    label: string;
    default_channels: string[];
    available_channels: string[];
  }[];
  locked: string[];
}

function settingsFromWire(w: SettingsWire): TenantSettings {
  return {
    general: {
      name: w.general.name,
      logoUrl: w.general.logo_url,
      address: w.general.address ?? "",
      website: w.general.website ?? "",
      country: w.general.country ?? "",
      defaultCurrency: w.general.default_currency ?? "",
      defaultLocale: w.general.default_locale,
      defaultTimezone: w.general.default_timezone,
    },
    security: {
      mfa: {
        mode: w.security.mfa.mode,
        requiredRoles: w.security.mfa.required_roles,
        maxAssuranceAgeHours: w.security.mfa.max_assurance_age_hours,
      },
      passwordPolicy: {
        minLength: w.security.password_policy.min_length,
        enforcement: w.security.password_policy.enforcement,
        graceDays: w.security.password_policy.grace_days,
      },
      session: {
        idleTimeoutMinutes: w.security.session.idle_timeout_minutes,
        absoluteMaxMinutes: w.security.session.absolute_max_minutes,
      },
      ipAllowlist: w.security.ip_allowlist,
      emailVerification: w.security.email_verification,
    },
    localisation: {
      platformLocales: w.localisation.platform_locales,
      availableLocales: w.localisation.available_locales,
      firstDayOfWeek: w.localisation.first_day_of_week,
      numberFormat: w.localisation.number_format,
    },
  };
}

// Drops the keys left undefined, so a PATCH carries only what changed.
function defined<T extends Record<string, unknown>>(obj: T): Partial<T> {
  return Object.fromEntries(Object.entries(obj).filter(([, v]) => v !== undefined)) as Partial<T>;
}

function nonEmpty<T extends Record<string, unknown>>(obj: T): T | undefined {
  return Object.keys(obj).length > 0 ? obj : undefined;
}

function settingsPatchToWire(p: TenantSettingsPatch): Record<string, unknown> {
  const g = p.general;
  const s = p.security;
  const l = p.localisation;
  return defined({
    general:
      g &&
      defined({
        name: g.name,
        address: g.address,
        website: g.website,
        country: g.country,
        default_currency: g.defaultCurrency,
        default_locale: g.defaultLocale,
        default_timezone: g.defaultTimezone,
      }),
    security:
      s &&
      nonEmpty(
        defined({
          mfa:
            s.mfa &&
            defined({
              mode: s.mfa.mode,
              required_roles: s.mfa.requiredRoles,
              max_assurance_age_hours: s.mfa.maxAssuranceAgeHours,
            }),
          password_policy:
            s.passwordPolicy &&
            defined({
              min_length: s.passwordPolicy.minLength,
              enforcement: s.passwordPolicy.enforcement,
              grace_days: s.passwordPolicy.graceDays,
            }),
          session:
            s.session &&
            defined({
              idle_timeout_minutes: s.session.idleTimeoutMinutes,
              absolute_max_minutes: s.session.absoluteMaxMinutes,
            }),
          ip_allowlist: s.ipAllowlist,
          email_verification: s.emailVerification,
        }),
      ),
    localisation:
      l &&
      defined({
        available_locales: l.availableLocales,
        first_day_of_week: l.firstDayOfWeek,
        number_format: l.numberFormat,
      }),
  });
}

function deliveryFromWire(w: DeliveryWire): NotificationDelivery {
  return {
    channels: {
      emailEnabled: w.channels.email_enabled,
      smsEnabled: w.channels.sms_enabled,
      pushEnabled: w.channels.push_enabled,
    },
    email: {
      provider: w.email.provider,
      apiKey: w.email.api_key,
      fromName: w.email.from_name,
      fromAddr: w.email.from_addr,
      replyTo: w.email.reply_to,
      layoutTemplate: w.email.layout_template,
      smtp: {
        host: w.email.smtp.host,
        port: w.email.smtp.port,
        user: w.email.smtp.user,
        password: w.email.smtp.password,
        useTLS: w.email.smtp.use_tls,
      },
    },
    sms: { senderId: w.sms.sender_id },
    defaults: w.defaults,
    types: w.types.map((t) => ({
      type: t.type,
      module: t.module,
      label: t.label,
      defaultChannels: t.default_channels,
      availableChannels: t.available_channels,
    })),
    locked: w.locked,
  };
}

function emailPatchToWire(e: EmailSettingsPatch): Record<string, unknown> {
  return defined({
    provider: e.provider,
    api_key: e.apiKey,
    from_name: e.fromName,
    from_addr: e.fromAddr,
    reply_to: e.replyTo,
    layout_template: e.layoutTemplate,
    smtp:
      e.smtp &&
      defined({
        host: e.smtp.host,
        port: e.smtp.port,
        user: e.smtp.user,
        password: e.smtp.password,
        use_tls: e.smtp.useTLS,
      }),
  });
}

function deliveryPatchToWire(p: NotificationDeliveryPatch): Record<string, unknown> {
  return defined({
    channels:
      p.channels &&
      defined({
        email_enabled: p.channels.emailEnabled,
        sms_enabled: p.channels.smsEnabled,
        push_enabled: p.channels.pushEnabled,
      }),
    email: p.email && emailPatchToWire(p.email),
    sms: p.sms && { sender_id: p.sms.senderId },
    defaults: p.defaults,
  });
}

const tenantSettingsKey = ["admin-settings"] as const;

export const tenantSettingsKeys = {
  all: tenantSettingsKey,
  settings: () => [...tenantSettingsKey, "settings"] as const,
  delivery: () => [...tenantSettingsKey, "notification-delivery"] as const,
};

export function useTenantSettings() {
  return useQuery({
    queryKey: tenantSettingsKeys.settings(),
    queryFn: async ({ signal }) => settingsFromWire(await apiClient.get<SettingsWire>("/admin/settings", { signal })),
  });
}

export function useUpdateTenantSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (patch: TenantSettingsPatch) =>
      settingsFromWire(await apiClient.patch<SettingsWire>("/admin/settings", settingsPatchToWire(patch))),
    onSuccess: (settings) => queryClient.setQueryData(tenantSettingsKeys.settings(), settings),
  });
}

function setLogo(queryClient: ReturnType<typeof useQueryClient>, logoUrl: string | null) {
  queryClient.setQueryData<TenantSettings>(
    tenantSettingsKeys.settings(),
    (settings) => settings && { ...settings, general: { ...settings.general, logoUrl } },
  );
}

export function useUploadLogo() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (file: File) =>
      (await apiClient.postFormData<{ logo_url: string }>("/admin/settings/logo", { file })).logo_url,
    onSuccess: (logoUrl) => setLogo(queryClient, logoUrl),
  });
}

export function useRemoveLogo() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      await apiClient.delete<{ logo_url: null }>("/admin/settings/logo");
    },
    onSuccess: () => setLogo(queryClient, null),
  });
}

export function useNotificationDelivery() {
  return useQuery({
    queryKey: tenantSettingsKeys.delivery(),
    queryFn: async ({ signal }) =>
      deliveryFromWire(await apiClient.get<DeliveryWire>("/admin/settings/notification-delivery", { signal })),
  });
}

export function useUpdateNotificationDelivery() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (patch: NotificationDeliveryPatch) =>
      deliveryFromWire(
        await apiClient.patch<DeliveryWire>("/admin/settings/notification-delivery", deliveryPatchToWire(patch)),
      ),
    onSuccess: (delivery) => queryClient.setQueryData(tenantSettingsKeys.delivery(), delivery),
  });
}

export function useSendTestEmail() {
  return useMutation({
    mutationFn: async (email: EmailSettingsPatch): Promise<TestEmailResult> => {
      const result = await apiClient.post<{ sent_to: string; provider: string }>(
        "/admin/settings/notification-delivery/test-email",
        { email: emailPatchToWire(email) },
      );
      return { sentTo: result.sent_to, provider: result.provider };
    },
  });
}
