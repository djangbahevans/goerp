import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";

// An in-memory stand-in for /admin/settings and
// /admin/settings/notification-delivery, installed over apiClient by the
// tenant settings tests and stories. Wire shapes, status codes and the
// secret-masking rules match internal/engine/auth/adminsettings.

type Json = Record<string, unknown>;

export interface FakeSettingsState {
  settings: Json;
  delivery: Json;
  // Plaintext secrets, which the wire never carries.
  secrets: { apiKey: string; smtpPassword: string };
}

export interface FakeTenantSettingsOptions {
  settings?: Json;
  delivery?: Json;
  secrets?: Partial<FakeSettingsState["secrets"]>;
  // Every settings request fails with a 500.
  failAll?: boolean;
  // Every test email fails with this 502 provider message.
  testEmailFailure?: string;
}

export interface FakeTenantSettingsBackend {
  state: () => FakeSettingsState;
  // Each request as "METHOD path" with its JSON body, in order.
  requests: { method: string; path: string; body: unknown }[];
  restore: () => void;
}

export const FAKE_ADMIN_EMAIL = "ada@acme.test";

export function defaultSettingsWire(): Json {
  return {
    general: {
      name: "Acme Ghana Ltd",
      logo_url: null,
      address: "1 Liberation Rd\nAccra",
      website: "https://acme.example",
      country: "GH",
      default_currency: "GHS",
      default_locale: "en",
      default_timezone: "Africa/Accra",
    },
    security: {
      mfa: { mode: "optional", required_roles: [], max_assurance_age_hours: 24 },
      password_policy: { min_length: 12, enforcement: "nudge", grace_days: 14, changed_at: null },
      session: { idle_timeout_minutes: 0, absolute_max_minutes: 0 },
      ip_allowlist: "",
      email_verification: { policy: "tenant_choice", required: true },
    },
    localisation: {
      platform_locales: ["en", "fr", "ar"],
      available_locales: ["en", "fr"],
      first_day_of_week: "monday",
      number_format: "1,234.56",
    },
  };
}

export function defaultDeliveryWire(): Json {
  return {
    channels: { email_enabled: true, sms_enabled: true, push_enabled: true },
    email: {
      provider: "resend",
      api_key: "",
      from_name: "",
      from_addr: "",
      reply_to: "",
      layout_template: "",
      smtp: { host: "", port: 587, user: "", password: "", use_tls: true },
    },
    sms: { sender_id: "" },
    defaults: {},
    types: [
      {
        type: "engine.activity_assigned",
        module: "engine",
        label: "Activity assigned to you",
        default_channels: ["in_app", "email"],
        available_channels: ["in_app", "email", "push"],
      },
      {
        type: "sales.order_confirmed",
        module: "sales",
        label: "Order confirmed",
        default_channels: ["in_app", "email"],
        available_channels: ["in_app", "email", "sms", "push"],
      },
      {
        type: "sales.quote_expiring",
        module: "sales",
        label: "Quote expiring",
        default_channels: ["in_app"],
        available_channels: ["in_app", "email"],
      },
    ],
    locked: [],
  };
}

function invalid(field: string, message: string): AppError {
  return new AppError({ code: "invalid_setting", message, httpStatus: 422, details: { field } });
}

function serverError(): AppError {
  return new AppError({ code: "internal_error", message: "request failed", httpStatus: 500 });
}

const EMAIL_PATTERN = /^[^\s@<>]+@[^\s@<>]+$/;

function isObject(v: unknown): v is Json {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// Deep-merges patch into target, the way a field-level PATCH applies.
function merge(target: Json, patch: Json): void {
  for (const [key, value] of Object.entries(patch)) {
    if (isObject(value) && isObject(target[key])) merge(target[key] as Json, value);
    else target[key] = value;
  }
}

function clone<T>(v: T): T {
  return structuredClone(v);
}

export function installFakeTenantSettingsBackend(options: FakeTenantSettingsOptions = {}): FakeTenantSettingsBackend {
  const client = apiClient as unknown as Record<string, unknown>;
  const original = {
    get: client.get,
    patch: client.patch,
    post: client.post,
    postFormData: client.postFormData,
    delete: client.delete,
  };
  const state: FakeSettingsState = {
    settings: options.settings ?? defaultSettingsWire(),
    delivery: options.delivery ?? defaultDeliveryWire(),
    secrets: { apiKey: "", smtpPassword: "", ...options.secrets },
  };
  const requests: FakeTenantSettingsBackend["requests"] = [];

  const deliveryResponse = (): Json => {
    const d = clone(state.delivery);
    const email = d.email as Json;
    email.api_key = state.secrets.apiKey ? "***" : "";
    (email.smtp as Json).password = state.secrets.smtpPassword ? "***" : "";
    return d;
  };

  const validateEmail = (email: Json): AppError | null => {
    if (email.provider !== undefined && email.provider !== "resend" && email.provider !== "smtp") {
      return invalid("email.provider", "provider is one of resend, smtp");
    }
    if (email.from_addr !== undefined && !EMAIL_PATTERN.test(String(email.from_addr))) {
      return invalid("email.from_addr", "the From address is an email address, such as noreply@example.com");
    }
    if (email.reply_to && !EMAIL_PATTERN.test(String(email.reply_to))) {
      return invalid("email.reply_to", "the reply-to address is an email address, such as support@example.com");
    }
    const port = isObject(email.smtp) ? email.smtp.port : undefined;
    if (port !== undefined && (typeof port !== "number" || port < 1 || port > 65535)) {
      return invalid("email.smtp.port", "the SMTP port is 1 to 65535");
    }
    return null;
  };

  // Applies an email patch's secrets to `secrets`, "***" keeping the stored one.
  const takeSecrets = (email: Json, secrets: FakeSettingsState["secrets"]) => {
    if (typeof email.api_key === "string" && email.api_key !== "***") secrets.apiKey = email.api_key;
    delete email.api_key;
    const smtp = email.smtp;
    if (isObject(smtp)) {
      if (typeof smtp.password === "string" && smtp.password !== "***") secrets.smtpPassword = smtp.password;
      delete smtp.password;
    }
  };

  const lockedChange = (patch: Json): string | null => {
    const locked = state.delivery.locked as string[];
    const current = deliveryResponse();
    const walk = (p: Json, c: Json, prefix: string): string | null => {
      for (const [key, value] of Object.entries(p)) {
        const field = prefix ? `${prefix}.${key}` : key;
        if (isObject(value) && isObject(c[key])) {
          const inner = walk(value, c[key] as Json, field);
          if (inner) return inner;
        } else if (locked.includes(field) && value !== c[key] && value !== "***") {
          return field;
        }
      }
      return null;
    };
    return walk(patch, current, "");
  };

  client.get = async (path: string) => {
    requests.push({ method: "GET", path, body: undefined });
    if (options.failAll) throw serverError();
    if (path === "/admin/settings") return clone(state.settings);
    if (path === "/admin/settings/notification-delivery") return deliveryResponse();
    if (path === "/admin/roles") {
      return {
        data: ["admin", "manager", "user"].map((name) => ({
          id: `role-${name}`,
          name,
          description: null,
          is_immutable: name !== "manager",
          user_count: 1,
          invitation_count: 0,
        })),
      };
    }
    return (original.get as (p: string) => Promise<unknown>).call(apiClient, path);
  };

  client.patch = async (path: string, body: Json) => {
    requests.push({ method: "PATCH", path, body: clone(body) });
    if (options.failAll) throw serverError();
    if (path === "/admin/settings") {
      const general = body.general as Json | undefined;
      if (general?.name !== undefined && String(general.name).trim() === "") {
        throw invalid("general.name", "a company name is 1 to 200 characters");
      }
      if (general?.website && !/^https?:\/\/\S+$/.test(String(general.website))) {
        throw invalid("general.website", "a website is an http or https URL");
      }
      const patch = clone(body);
      for (const key of ["name", "address", "website"]) {
        const g = patch.general as Json | undefined;
        if (typeof g?.[key] === "string") g[key] = (g[key] as string).trim();
      }
      const next = clone(state.settings);
      merge(next, patch);
      state.settings = next;
      return clone(state.settings);
    }
    if (path === "/admin/settings/notification-delivery") {
      const patch = clone(body);
      if (isObject(patch.email)) {
        const err = validateEmail(patch.email);
        if (err) throw err;
      }
      const locked = lockedChange(patch);
      if (locked) {
        throw new AppError({
          code: "locked_setting",
          message: "this setting is set by your platform operator",
          httpStatus: 422,
          details: { field: locked },
        });
      }
      const sender = isObject(patch.sms) ? patch.sms.sender_id : undefined;
      if (typeof sender === "string" && sender !== "" && !/^([A-Za-z0-9]{1,11}|\+[1-9][0-9]{1,14})$/.test(sender)) {
        throw invalid("sms.sender_id", "a sender ID is 1 to 11 letters and digits, or a phone number in E.164 form");
      }
      const secrets = { ...state.secrets };
      if (isObject(patch.email)) takeSecrets(patch.email, secrets);
      state.secrets = secrets;
      // defaults merge by type, and null returns a type to its manifest defaults.
      const defaults = { ...(state.delivery.defaults as Json) };
      for (const [type, channels] of Object.entries(isObject(patch.defaults) ? patch.defaults : {})) {
        if (channels === null) delete defaults[type];
        else defaults[type] = channels;
      }
      delete patch.defaults;
      merge(state.delivery, patch);
      state.delivery.defaults = defaults;
      return deliveryResponse();
    }
    return (original.patch as (p: string, b: unknown) => Promise<unknown>).call(apiClient, path, body);
  };

  client.post = async (path: string, body: Json) => {
    requests.push({ method: "POST", path, body: clone(body) });
    if (path !== "/admin/settings/notification-delivery/test-email") {
      return (original.post as (p: string, b: unknown) => Promise<unknown>).call(apiClient, path, body);
    }
    const email = clone((body.email as Json | undefined) ?? {});
    const err = validateEmail(email);
    if (err) throw err;
    const secrets = { ...state.secrets };
    takeSecrets(email, secrets);
    const effective = clone(state.delivery.email as Json);
    merge(effective, email);
    if (!effective.from_addr) throw invalid("email.from_addr", "a From address is required to send email");
    if (effective.provider === "resend" && !secrets.apiKey) {
      throw invalid("email.api_key", "an API key is required to send email through Resend");
    }
    if (effective.provider === "smtp" && !(effective.smtp as Json).host) {
      throw invalid("email.smtp.host", "an SMTP host is required to send email through SMTP");
    }
    if (options.testEmailFailure) {
      throw new AppError({
        code: "test_email_failed",
        message: "the email provider did not accept the test email",
        httpStatus: 502,
        details: { message: options.testEmailFailure },
      });
    }
    return { sent_to: FAKE_ADMIN_EMAIL, provider: effective.provider };
  };

  client.postFormData = async (path: string, data: Json) => {
    requests.push({ method: "POST", path, body: undefined });
    if (path !== "/admin/settings/logo") {
      return (original.postFormData as (p: string, d: unknown) => Promise<unknown>).call(apiClient, path, data);
    }
    const file = data.file as Blob;
    if (!/^image\/(png|jpeg|gif|webp)$/.test(file.type)) {
      throw new AppError({ code: "invalid_content_type", message: "unsupported image type", httpStatus: 415 });
    }
    const url = `data:${file.type};base64,${btoa(String.fromCharCode(...new Uint8Array(await file.arrayBuffer())))}`;
    (state.settings.general as Json).logo_url = url;
    return { logo_url: url };
  };

  client.delete = async (path: string) => {
    requests.push({ method: "DELETE", path, body: undefined });
    if (path !== "/admin/settings/logo") {
      return (original.delete as (p: string) => Promise<unknown>).call(apiClient, path);
    }
    (state.settings.general as Json).logo_url = null;
    return { logo_url: null };
  };

  return {
    state: () => state,
    requests,
    restore: () => {
      Object.assign(client, original);
    },
  };
}
