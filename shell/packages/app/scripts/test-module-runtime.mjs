import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { promisify } from "node:util";
import { chromium } from "playwright";
import { build, createServer } from "vite";
import { SHARED_PACKAGES, sharedPackages } from "../src/dev-server/shared-packages.ts";

const run = promisify(execFile);
const repo = resolve(import.meta.dirname, "../../../..");
const workspace = resolve(repo, "shell");
const temp = await mkdtemp(resolve(tmpdir(), "goerp-module-runtime-"));
let browser;
let dev;
let production;

try {
  await symlink(resolve(workspace, "node_modules"), resolve(temp, "node_modules"), "dir");
  await mkdir(resolve(temp, "module"));
  await run(
    "go",
    [
      "run",
      "./cmd/goerp",
      "codegen",
      ".agents/skills/run-goerp/sample-crm",
      "--local",
      "--output",
      resolve(temp, "module/generated.ts"),
    ],
    { cwd: repo },
  );
  await writeFile(
    resolve(temp, "module/index.ts"),
    `
import { apiClient, defineModule } from '@goerp/sdk';
import { useModule } from '@goerp/sdk/react';
import { useQuery } from '@tanstack/react-query';
import { createElement, lazy, useState } from 'react';
import { crmApi } from './generated';
function RuntimeCheck() {
  const context = useModule('crm');
  const [count, setCount] = useState(0);
  const result = useQuery({ queryKey: ['runtime-check'], queryFn: () => context.api.listContacts() });
  window.runtimeIdentity = {
    react: useState === window.hostRuntime.useState,
    client: apiClient === window.hostRuntime.apiClient,
    query: context.queryClient === window.hostRuntime.queryClient,
  };
  return createElement('button', { onClick: () => setCount(count + 1) },
    result.data ? result.data.data[0].name + ':' + count : 'Loading');
}
export default defineModule({ name: 'crm', api: crmApi, views: { RuntimeCheck: lazy(() => Promise.resolve({ default: RuntimeCheck })) } });
`,
  );
  await build({
    configFile: false,
    root: resolve(temp, "module"),
    logLevel: "warn",
    build: {
      lib: { entry: "index.ts", formats: ["es"], fileName: () => "bundle.js" },
      rolldownOptions: { external: SHARED_PACKAGES },
    },
  });
  const bundle = await readFile(resolve(temp, "module/dist/bundle.js"));
  const hash = `sha256:${createHash("sha256").update(bundle).digest("hex")}`;
  const loader = resolve(repo, "shell/packages/app/src/bootstrap/module-loader.ts");
  const importMapWatcher = resolve(repo, "shell/packages/app/src/bootstrap/import-map.ts");
  await writeFile(
    resolve(temp, "index.html"),
    '<head><!--goerp-import-map--></head><body><div id="root"></div><script type="module" src="/host.js"></script></body>',
  );
  await writeFile(
    resolve(temp, "host.js"),
    `
import { createElement, Suspense, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { apiClient } from '@goerp/sdk';
import { AuthContext, PermissionContext } from '@goerp/sdk/auth';
import { ModuleNavigationProvider } from '@goerp/sdk/react';
import { componentRegistry } from '@goerp/sdk/schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ensureModuleRegistered } from ${JSON.stringify(loader)};
import { watchImportMap } from ${JSON.stringify(importMapWatcher)};
watchImportMap();
const queryClient = new QueryClient();
window.hostRuntime = { useState, apiClient, queryClient };
window.registerBundle = (url, hash) => ensureModuleRegistered('crm', url, hash);
await window.registerBundle('/__fixture_bundle.js', ${JSON.stringify(hash)});
const Component = componentRegistry.resolve('RuntimeCheck');
createRoot(document.getElementById('root')).render(
  createElement(QueryClientProvider, { client: queryClient },
    createElement(AuthContext.Provider, { value: { user: { id: 'user' }, tenant: { id: 'tenant' } } },
      createElement(PermissionContext.Provider, { value: { check: () => true } },
        createElement(ModuleNavigationProvider, { navigate: () => {} },
          createElement(Suspense, { fallback: 'Loading' }, createElement(Component)))))));
`,
  );
  const schema = {
    engine_version: "test",
    schema_hash: "test",
    modules: {
      crm: {
        name: "crm",
        display_name: "CRM",
        version: "0.1.0",
        views: [],
        navigation: [],
        models: {},
        permissions: [],
        view_extensions: [],
        view_extension_definitions: [],
        load_order: 0,
        frontend: null,
        public_config: {},
        notification_types: [],
        routes: [
          {
            method: "GET",
            path: "/crm/contacts",
            permissions: [],
            model: "crm.contact",
            crud_action: "list",
            engine_native: true,
            response_is_list: true,
          },
          {
            method: "GET",
            path: "/crm/search",
            permissions: [],
            model: "crm.contact",
            crud_action: "list",
            response_is_list: true,
          },
        ],
      },
    },
  };
  browser = await chromium.launch();
  async function verify(url, label) {
    const page = await browser.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.route("**/__fixture_bundle.js", (route) =>
      route.fulfill({ contentType: "text/javascript", body: bundle }),
    );
    await page.route("**/_meta/schema", (route) => route.fulfill({ json: schema }));
    await page.route("**/modules/crm/translations/*", (route) => route.fulfill({ json: {} }));
    await page.route("**/crm/contacts*", (route) =>
      route.fulfill({ json: { data: [{ name: "Kofi Mensah" }], meta: { has_more: false } } }),
    );
    await page.route("**/crm/search*", (route) => {
      errors.push("Generated CRUD client called the raw field-security route");
      return route.fulfill({ json: { data: [], meta: { has_more: false } } });
    });
    await page.goto(url);
    await page.getByRole("button", { name: "Kofi Mensah:0" }).waitFor();
    assert.deepEqual(await page.evaluate(() => window.runtimeIdentity), { react: true, client: true, query: true });
    await page.getByRole("button").click();
    await page.getByRole("button", { name: "Kofi Mensah:1" }).waitFor();
    const mappings = await page.evaluate(
      () => JSON.parse(document.getElementById("goerp-import-map").textContent).imports,
    );
    assert.deepEqual(Object.keys(mappings).sort(), [...SHARED_PACKAGES].sort());
    for (const name of SHARED_PACKAGES) await page.evaluate((specifier) => import(specifier), name);
    await page.evaluate(() => {
      window.documentIdentity = crypto.randomUUID();
    });
    const identity = await page.evaluate(() => window.documentIdentity);
    await page.evaluate(async () => {
      const source = "import { defineModule } from '@goerp/sdk'; export default defineModule({ name: 'crm' });";
      const bytes = new TextEncoder().encode(source);
      const digest = await crypto.subtle.digest("SHA-256", bytes);
      const hash = `sha256:${Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("")}`;
      const url = URL.createObjectURL(new Blob([source], { type: "text/javascript" }));
      try {
        await window.registerBundle(url, hash);
      } finally {
        URL.revokeObjectURL(url);
      }
    });
    assert.equal(await page.evaluate(() => window.documentIdentity), identity);
    await page.route("**/__goerp_import_map", (route) =>
      route.fulfill({ json: { imports: { ...mappings, react: `${mappings.react}?next` } } }),
    );
    await Promise.all([
      page.waitForEvent("load"),
      page.evaluate(() => document.dispatchEvent(new Event("visibilitychange"))),
    ]);
    assert.equal(await page.evaluate(() => window.documentIdentity), undefined);
    assert.deepEqual(errors, []);
    await page.close();
    console.log(
      `${label}: shared runtime instances, generated API, package exports, module hot reload and shared-package reload passed`,
    );
  }
  dev = await createServer({
    configFile: false,
    root: temp,
    logLevel: "warn",
    plugins: [sharedPackages()],
    server: { host: "127.0.0.1", port: 0, fs: { allow: [repo, temp] } },
  });
  await dev.listen();
  await verify(dev.resolvedUrls.local[0], "Vite development");
  await dev.close();
  dev = undefined;
  await build({
    configFile: false,
    root: temp,
    logLevel: "warn",
    plugins: [sharedPackages()],
    build: { target: "esnext" },
  });
  const binary = resolve(temp, "serve-shell");
  await run("go", ["build", "-o", binary, "./internal/engine/shellassets/testdata/server"], { cwd: repo });
  production = spawn(binary, [resolve(temp, "dist")], { stdio: ["ignore", "pipe", "inherit"] });
  const url = await new Promise((accept, reject) => {
    production.stdout.once("data", (data) => accept(data.toString().trim()));
    production.once("error", reject);
    production.once("exit", (code) => reject(new Error(`Shell server exited: ${code}`)));
  });
  await verify(url, "Engine production handler");
} finally {
  await browser?.close();
  await dev?.close();
  production?.kill("SIGTERM");
  await rm(temp, { recursive: true, force: true });
}
