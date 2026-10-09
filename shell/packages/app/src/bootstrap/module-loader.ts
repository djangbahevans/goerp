import { developmentModuleName, loadDevelopmentModule } from "virtual:goerp-module-development";
import type { ModuleDefinition } from "@goerp/sdk";
import { translationLoader } from "@goerp/sdk/i18n";
import { registerModule } from "./register-module.js";

function hexEncode(bytes: ArrayBuffer): string {
  return Array.from(new Uint8Array(bytes))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

async function defaultImporter(bytes: ArrayBuffer): Promise<unknown> {
  const blob = new Blob([bytes], { type: "text/javascript" });
  const blobUrl = URL.createObjectURL(blob);

  try {
    // Blob imports execute only the bytes whose manifest checksum was verified.
    return await import(/* @vite-ignore */ blobUrl);
  } finally {
    URL.revokeObjectURL(blobUrl);
  }
}

export interface LoadVerifiedModuleOptions {
  importer?: (bytes: ArrayBuffer) => Promise<unknown>;
}

/**
 * Fetches a module frontend bundle, verifies its SHA-256 against the
 * manifest's declared frontend.bundle_sha256, and only then imports it.
 * A hash mismatch throws before the importer is ever called.
 */
export async function loadVerifiedModule(
  bundleUrl: string,
  expectedSha256: string,
  options?: LoadVerifiedModuleOptions,
): Promise<unknown> {
  const response = await fetch(bundleUrl);
  if (!response.ok) {
    throw new Error(`failed to fetch module bundle ${bundleUrl}: ${response.status} ${response.statusText}`);
  }

  const bytes = await response.arrayBuffer();
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  const actualHex = hexEncode(digest);
  const expectedHex = expectedSha256.replace(/^sha256:/, "");

  if (actualHex !== expectedHex) {
    throw new Error(
      `module bundle ${bundleUrl} failed integrity verification: expected sha256:${expectedHex}, got sha256:${actualHex}`,
    );
  }

  const importer = options?.importer ?? defaultImporter;
  return importer(bytes);
}

const loaded = new Map<string, Promise<unknown>>();

export async function ensureLoaded(
  moduleName: string,
  bundleUrl: string | null,
  bundleSha256: string | null,
  options?: LoadVerifiedModuleOptions,
): Promise<unknown | null> {
  if (import.meta.env.DEV && moduleName === developmentModuleName) return loadDevelopmentModule();
  if (!bundleUrl || !bundleSha256) return null;

  const key = `${moduleName}:${bundleUrl}:${bundleSha256}`;
  const existing = loaded.get(key);
  if (existing) return existing;

  const promise = loadVerifiedModule(bundleUrl, bundleSha256, options).catch((err: unknown) => {
    loaded.delete(key);
    throw err;
  });
  loaded.set(key, promise);
  return promise;
}

function asModuleDefinition(moduleName: string, loadedModule: unknown): ModuleDefinition {
  const definition = (loadedModule as { default?: unknown } | null)?.default;
  if (!definition || typeof definition !== "object" || typeof (definition as { name?: unknown }).name !== "string") {
    throw new Error(`module bundle for "${moduleName}" has no valid defineModule() default export`);
  }
  return definition as ModuleDefinition;
}

const unregisterCommands = new Map<string, () => void>();

const registered = new Map<string, Promise<void>>();

// A slower import must not replace a registration from a newer bundle.
const latestKeyForModule = new Map<string, string>();

export async function ensureModuleRegistered(
  moduleName: string,
  bundleUrl: string | null,
  bundleSha256: string | null,
  options?: LoadVerifiedModuleOptions,
): Promise<void> {
  if (!bundleUrl || !bundleSha256) return;

  const key = `${moduleName}:${bundleUrl}:${bundleSha256}`;
  latestKeyForModule.set(moduleName, key);
  const existing = registered.get(key);
  if (existing) return existing;

  const translations = translationLoader.load(moduleName);
  const promise = Promise.all([ensureLoaded(moduleName, bundleUrl, bundleSha256, options), translations])
    .then(([loadedModule]) => {
      if (!loadedModule) return;
      if (latestKeyForModule.get(moduleName) !== key) return;
      unregisterCommands.get(moduleName)?.();
      unregisterCommands.set(moduleName, registerModule(asModuleDefinition(moduleName, loadedModule)));
    })
    .catch((err: unknown) => {
      registered.delete(key);
      throw err;
    });
  registered.set(key, promise);
  return promise;
}
