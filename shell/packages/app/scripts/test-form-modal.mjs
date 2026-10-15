import assert from "node:assert/strict";
import { mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { chromium } from "playwright";
import { createServer } from "vite";
import { moduleDevelopment } from "../src/dev-server/module-development.ts";
import { sharedPackages } from "../src/dev-server/shared-packages.ts";

const repo = resolve(import.meta.dirname, "../../../..");
const temp = await mkdtemp(resolve(tmpdir(), "goerp-form-modal-"));
const app = (path) => JSON.stringify(resolve(repo, "shell/packages/app/src", path));

const route = (overrides) => ({ method: "GET", path: "", permissions: [], response_is_list: false, ...overrides });
const schema = {
  engine_version: "test",
  schema_hash: "form-modal",
  modules: {
    contacts: {
      name: "contacts",
      version: "0.1.0",
      display_name: "Contacts",
      models: {
        "contacts.contact": {
          name: "contact",
          label: "Contact",
          label_plural: "Contacts",
          fields: [{ name: "name", type: "text" }],
          enabled_ops: [],
          shareable: false,
        },
      },
      views: [
        { name: "contacts_list", type: "list", resource: "contacts.contact", label: "Contacts", columns: [] },
        {
          name: "contacts_form",
          type: "form",
          resource: "contacts.contact",
          label: "Contact",
          chatter: false,
          sections: [{ type: "fields", fields: [{ field: "name", label: "Name", type: "text" }] }],
        },
      ],
      navigation: [],
      permissions: [],
      frontend: null,
      view_extensions: [],
      view_extension_definitions: [],
      error_codes: [],
      notification_types: [],
      load_order: 0,
      public_config: {},
      routes: [
        route({
          path: "/contacts",
          model: "contacts.contact",
          crud_action: "list",
          engine_native: true,
          view: "contacts_list",
        }),
        route({
          path: "/contacts/{id}",
          model: "contacts.contact",
          crud_action: "get",
          engine_native: true,
          view: "contacts_form",
        }),
        route({
          method: "POST",
          path: "/contacts",
          model: "contacts.contact",
          crud_action: "create",
          engine_native: true,
          view: "contacts_form",
        }),
        route({
          method: "PUT",
          path: "/contacts/{id}",
          model: "contacts.contact",
          crud_action: "update",
          engine_native: true,
        }),
      ],
    },
  },
};

let browser;
let server;

try {
  await symlink(resolve(repo, "shell/node_modules"), resolve(temp, "node_modules"), "dir");
  await writeFile(
    resolve(temp, "index.html"),
    '<head><!--goerp-import-map--></head><body><div id="root"></div><script type="module" src="/fixture.tsx"></script></body>',
  );
  await writeFile(
    resolve(temp, "fixture.tsx"),
    `
import React from 'react';
import { createRoot } from 'react-dom/client';
import { AuthContext, PermissionContext, createPermissionContextValue } from '@goerp/sdk/auth';
import { executeInstructions, currentExecutionContext, useExecutionContext } from '@goerp/sdk/realtime';
import { buildViewRegistry, ViewRegistryContext } from '@goerp/sdk/schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRootRoute, createRoute, createRouter, createBrowserHistory, RouterProvider, Outlet } from '@tanstack/react-router';
import { configureUIExecutor } from '@goerp/sdk/realtime';
import { ModalRoot } from ${app("chrome/modal-root.tsx")};
const schema = ${JSON.stringify(schema)};
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const open = (ui) => executeInstructions(ui, 'response');
function Page() {
  useExecutionContext({ view: 'list', resource: 'contacts.contact', recordId: null, record: null, queryKey: ['contacts-page'] });
  return <main>
    <h1>Contacts page</h1>
    <button type="button" onClick={() => void open([{ type: 'open_form', resource: 'contacts.contact', record_id: '01j' }])}>Open contact</button>
    <button type="button" onClick={() => void open([{ type: 'open_form', resource: 'contacts.contact' }])}>New contact</button>
  </main>;
}
const root = createRootRoute({ component: () => <><Outlet /><ModalRoot /></> });
const page = createRoute({ getParentRoute: () => root, path: '/', component: Page });
const router = createRouter({ routeTree: root.addChildren([page]), history: createBrowserHistory() });
configureUIExecutor({ queryClient, navigate: href => router.navigate({ href }) });
window.uiTest = { executeInstructions, currentExecutionContext };
const auth = { tenant: { defaultCurrency: 'GHS' }, user: { id: 'u1', roles: [] } };
const permissions = createPermissionContextValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set(['contacts']) });
createRoot(document.getElementById('root')).render(
  <QueryClientProvider client={queryClient}><AuthContext.Provider value={auth}><PermissionContext value={permissions}>
    <ViewRegistryContext value={buildViewRegistry(schema)}><RouterProvider router={router} /></ViewRegistryContext>
  </PermissionContext></AuthContext.Provider></QueryClientProvider>);
`,
  );
  server = await createServer({
    configFile: false,
    root: temp,
    logLevel: "warn",
    plugins: [sharedPackages(), moduleDevelopment()],
    esbuild: { jsx: "automatic" },
    server: { host: "127.0.0.1", port: 0, fs: { allow: [repo, temp] } },
  });
  await server.listen();
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  const errors = [];
  const requests = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/*", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/_meta/schema") {
      await route.fulfill({ json: schema });
    } else if (path === "/contacts" && request.method() === "POST") {
      requests.push(`POST ${path} ${request.postData()}`);
      await route.fulfill({ status: 201, json: { id: "01new", ...request.postDataJSON() } });
    } else if (path.startsWith("/contacts/")) {
      requests.push(`${request.method()} ${path}`);
      const id = path.split("/").at(-1);
      await route.fulfill({ json: { id, name: id === "01j" ? "Ada" : "Grace" } });
    } else if (path.startsWith("/_meta/") || path.startsWith("/ws")) {
      await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "not found" } } });
    } else {
      await route.continue();
    }
  });
  await page.goto(server.resolvedUrls.local[0]);
  await page.getByRole("heading", { name: "Contacts page" }).waitFor();
  const dialog = page.getByRole("dialog", { name: "Contact" });
  const focusInDialog = () => page.evaluate(() => Boolean(document.activeElement?.closest('[role="dialog"]')));
  const focusedText = () => page.evaluate(() => document.activeElement?.textContent ?? "");
  const context = () => page.evaluate(() => window.uiTest.currentExecutionContext());

  await page.getByRole("button", { name: "Open contact" }).click();
  await dialog.getByText("Ada").waitFor();
  assert.ok(await focusInDialog(), "focus moves into the modal");
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press("Tab");
    assert.ok(await focusInDialog(), "Tab stays inside the modal");
  }
  assert.equal((await context()).recordId, "01j", "the modal's form is the current execution context");
  const reads = () => requests.filter((r) => r === "GET /contacts/01j").length;
  const before = reads();
  await page.evaluate(() =>
    window.uiTest.executeInstructions([{ type: "reload", resource: "contacts.contact", record_id: "01j" }], "response"),
  );
  assert.equal(reads(), before + 1, "a reload targeting the modal's record reloads it");

  await dialog.getByRole("button", { name: "Edit" }).click();
  assert.ok(new URL(page.url()).search === "", "edit mode leaves the page URL alone");
  await dialog.getByRole("textbox").fill("Ada Lovelace");
  await page.keyboard.press("Escape");
  const confirm = page.getByRole("alertdialog");
  await confirm.getByText("Leave without saving?").waitFor();
  await confirm.getByRole("button", { name: "Stay" }).click();
  await confirm.waitFor({ state: "detached" });
  assert.equal(await dialog.getByRole("textbox").inputValue(), "Ada Lovelace", "Stay keeps the edit");
  await page.keyboard.press("Escape");
  await confirm.getByRole("button", { name: "Leave" }).click();
  await dialog.waitFor({ state: "detached" });
  await page.waitForFunction(() => document.activeElement?.textContent === "Open contact");
  assert.equal(await focusedText(), "Open contact", "focus returns to the triggering control");
  assert.deepEqual(
    { view: (await context()).view, recordId: (await context()).recordId },
    { view: "list", recordId: null },
    "the page's context applies again after close",
  );

  await page.getByRole("button", { name: "New contact" }).click();
  await dialog.getByRole("textbox").fill("Grace");
  await dialog.getByRole("button", { name: "Save" }).click();
  await dialog.getByRole("button", { name: "Edit" }).waitFor();
  assert.ok(requests.includes('POST /contacts {"name":"Grace"}'), "saving creates the record");
  assert.equal((await context()).recordId, "01new", "the modal moves to the created record");
  assert.equal(new URL(page.url()).pathname, "/", "the page underneath stays put");
  await page.getByText("Contacts page", { exact: true }).waitFor();

  await page.evaluate(() => window.uiTest.executeInstructions([{ type: "close_modal" }], "response"));
  await dialog.waitFor({ state: "detached" });
  assert.deepEqual(errors, []);
  console.log("Form modal browser checks passed: open, focus trap and return, reload, leave guard, create in place");
} finally {
  await browser?.close();
  await server?.close();
  await rm(temp, { recursive: true, force: true });
}
