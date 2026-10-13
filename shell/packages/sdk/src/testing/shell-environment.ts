import type { CurrentUser } from "../auth/types.js";
import { DEFAULT_TENANT_FORMAT, tenantFormatStore } from "../i18n/tenant-format.js";
import { localeStore } from "../i18n/use-locale.js";
import { themeStore } from "../react/use-theme.js";

interface Environment {
  fetch: typeof fetch;
  user: CurrentUser;
}

const environments: Environment[] = [];
let restore: (() => void) | undefined;

function captureEnvironment(): () => void {
  const originalFetch = globalThis.fetch;
  const locale = localeStore.getLocale();
  const theme = themeStore.getPreference();
  const contrast = themeStore.getContrastPreference();
  const tenantFormat = tenantFormatStore.get();
  const storage = ["goerp-locale", "goerp-theme", "goerp-contrast"].map(
    (key) => [key, localStorage.getItem(key)] as const,
  );
  const attributes = ["lang", "dir", "data-theme", "data-contrast"].map(
    (name) => [name, document.documentElement.getAttribute(name)] as const,
  );

  return () => {
    globalThis.fetch = originalFetch;
    localeStore.setLocale(locale);
    themeStore.setPreference(theme);
    themeStore.setContrastPreference(contrast);
    tenantFormatStore.set(tenantFormat);
    for (const [key, value] of storage) {
      if (value === null) localStorage.removeItem(key);
      else localStorage.setItem(key, value);
    }
    for (const [name, value] of attributes) {
      if (value === null) document.documentElement.removeAttribute(name);
      else document.documentElement.setAttribute(name, value);
    }
  };
}

function applyEnvironment(environment: Environment): void {
  globalThis.fetch = environment.fetch;
  localeStore.setLocale(environment.user.locale ?? "en");
  themeStore.setPreference(environment.user.theme);
  themeStore.setContrastPreference(environment.user.contrast);
  tenantFormatStore.set(DEFAULT_TENANT_FORMAT);
}

export function mountShellEnvironment(environment: Environment): () => void {
  Intl.getCanonicalLocales(environment.user.locale ?? "en");
  // Fetch and appearance stores are browser globals; the last mounted shell owns them.
  if (environments.length === 0) restore = captureEnvironment();
  environments.push(environment);
  applyEnvironment(environment);

  return () => {
    const index = environments.indexOf(environment);
    if (index === -1) return;
    const wasActive = index === environments.length - 1;
    environments.splice(index, 1);
    if (!wasActive) return;
    const active = environments.at(-1);
    if (active) applyEnvironment(active);
    else {
      restore?.();
      restore = undefined;
    }
  };
}
