import assert from "node:assert/strict";
import { mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { chromium } from "playwright";
import { createServer } from "vite";
import { sharedPackages } from "../src/dev-server/shared-packages.ts";

const repo = resolve(import.meta.dirname, "../../../..");
const temp = await mkdtemp(resolve(tmpdir(), "goerp-ui-instructions-"));
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
import { apiClient, callAction } from '@goerp/sdk';
import { AuthContext, PermissionContext, createPermissionContextValue } from '@goerp/sdk/auth';
import { configureUIExecutor, executeInstructions, currentExecutionContext, useExecutionContext, modalManager } from '@goerp/sdk/realtime';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { createRootRoute, createRoute, createRouter, createBrowserHistory, RouterProvider, Outlet } from '@tanstack/react-router';
import { ModalRoot } from ${JSON.stringify(resolve(repo, "shell/packages/app/src/chrome/modal-root.tsx"))};
import { NavBadge } from ${JSON.stringify(resolve(repo, "shell/packages/app/src/chrome/nav-badge.tsx"))};
import { ListActions } from ${JSON.stringify(resolve(repo, "shell/packages/app/src/renderers/list/list-actions.tsx"))};
import { BulkActions } from ${JSON.stringify(resolve(repo, "shell/packages/app/src/renderers/list/bulk-actions.tsx"))};
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function View({ id }) {
  const queryKey = id ? ['record', 'sales.invoice', id] : ['infinite-list', null, 'sales.invoice'];
  const { data } = useQuery({ queryKey, staleTime: Infinity, queryFn: () => apiClient.get(id ? '/record/' + id : '/records') });
  useExecutionContext({ view: id ? 'form' : 'list', resource: 'sales.invoice', recordId: id ?? null, record: data ?? null, queryKey });
  return <h1>{id ? 'Invoice ' + id : 'Invoices'}</h1>;
}
function Root() {
  return <><NavBadge route="/count" /><Outlet />
    <ListActions module="sales" actions={[
      { type: 'route', label: 'Header envelope', route: 'sales.envelope' },
      { type: 'route', label: 'Header plain', route: 'sales.plain' },
      { type: 'route', label: 'Request Payment', route: 'sales.momo' },
      { type: 'menu', label: 'More', items: [{ type: 'route', label: 'Menu envelope', route: 'sales.envelope' }] },
    ]} />
    <BulkActions actions={[{ type: 'route', label: 'Bulk envelope', route: 'sales.envelope' }]} selectedIds={['01j']} clearSelection={() => { window.bulkCompleted = true; }} />
    <ModalRoot renderForm={config => <View id={config.recordId} />} />
  </>;
}
const root = createRootRoute({ component: Root });
const list = createRoute({ getParentRoute: () => root, path: '/', component: () => <View /> });
const form = createRoute({ getParentRoute: () => root, path: '/_m/sales/invoices/01j', component: () => <View id="01j" />, beforeLoad: () => new Promise(resolve => setTimeout(resolve, 50)) });
const router = createRouter({ routeTree: root.addChildren([list, form]), history: createBrowserHistory() });
configureUIExecutor({ queryClient, navigate: href => router.navigate({ href }) });
window.uiTest = { executeInstructions, currentExecutionContext, modalManager, callAction, queryClient };
createRoot(document.getElementById('root')).render(<QueryClientProvider client={queryClient}><AuthContext.Provider value={{ tenant: { defaultCurrency: 'GHS' } }}><PermissionContext value={createPermissionContextValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set(['sales']) })}><RouterProvider router={router} /></PermissionContext></AuthContext.Provider></QueryClientProvider>);
`,
  );
  server = await createServer({
    configFile: false,
    root: temp,
    logLevel: "warn",
    plugins: [sharedPackages()],
    esbuild: { jsx: "automatic" },
    server: { host: "127.0.0.1", port: 0, fs: { allow: [repo, temp] } },
  });
  await server.listen();
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  const errors = [];
  const requests = [];
  const momoBodies = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/*", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/_meta/schema") {
      await route.fulfill({
        json: {
          engine_version: "test",
          schema_hash: "ui-instructions",
          modules: {
            sales: {
              name: "sales",
              version: "0.1.0",
              display_name: "Sales",
              models: {},
              views: [],
              navigation: [],
              permissions: [],
              frontend: null,
              view_extensions: [],
              view_extension_definitions: [],
              error_codes: [],
              notification_types: [],
              load_order: 0,
              public_config: {},
              routes: ["envelope", "plain", "momo"].map((name) => ({
                name,
                method: "POST",
                path: `/action/${name}`,
                permissions: [],
                response_is_list: false,
              })),
            },
          },
        },
      });
    } else if (path === "/count") {
      await route.fulfill({ json: { count: 1 } });
    } else if (path === "/records" || path.startsWith("/record/")) {
      requests.push(path);
      await route.fulfill({ json: { id: path.split("/").at(-1) } });
    } else if (path === "/action/momo") {
      const body = route.request().postDataJSON();
      momoBodies.push(body);
      const ui =
        body === null
          ? [
              {
                type: "open_dialog",
                title: "Request MoMo Payment",
                submit_label: "Send Request",
                action: { method: "POST", path: "/action/momo" },
                fields: [
                  { name: "phone", label: "MoMo Number", type: "phone", required: true },
                  { name: "amount", label: "Amount", type: "monetary", required: true },
                ],
                values: { phone: "0244123456", amount: 50000 },
              },
              { type: "set_badge", target: "/count", count: 3 },
            ]
          : body.phone === "0000"
            ? null
            : [{ type: "close_modal" }, { type: "toast", message: "Payment request sent", severity: "success" }];
      if (ui === null) {
        await route.fulfill({
          status: 400,
          json: { error: { code: "payment.invalid_phone", message: "Not a MoMo number" } },
        });
      } else {
        await route.fulfill({ json: { ui }, headers: { "X-GoERP-UI": "1" } });
      }
    } else if (path.startsWith("/action/")) {
      const body = { data: { saved: true }, ui: [{ type: "set_badge", target: "/count", count: 7 }] };
      await route.fulfill({ json: body, headers: path.endsWith("/envelope") ? { "X-GoERP-UI": "1" } : {} });
    } else {
      await route.continue();
    }
  });
  await page.goto(server.resolvedUrls.local[0]);
  await page.getByRole("heading", { name: "Invoices", exact: true }).waitFor();
  await page.getByText("1", { exact: true }).waitFor();
  const beforeEnvelope = requests.length;
  await page.getByRole("button", { name: "Header envelope", exact: true }).click();
  await page.getByText("7", { exact: true }).waitFor();
  assert.equal(requests.length, beforeEnvelope, "an envelope does not implicitly reload");
  await page.getByRole("button", { name: "More", exact: true }).click();
  await page.getByRole("menuitem", { name: "Menu envelope", exact: true }).click();
  await page.getByRole("button", { name: "Bulk envelope", exact: true }).click();
  await page.waitForFunction(() => window.bulkCompleted === true);
  const trigger = page.getByRole("button", { name: "Request Payment", exact: true });
  const dialog = page.getByRole("dialog", { name: "Request MoMo Payment" });
  const focusInDialog = () => page.evaluate(() => Boolean(document.activeElement?.closest('[role="dialog"]')));
  const triggerFocused = () => page.evaluate(() => document.activeElement?.textContent === "Request Payment");

  await trigger.click();
  await dialog.waitFor();
  assert.ok(await focusInDialog(), "focus moves into the dialog");
  for (let i = 0; i < 8; i++) {
    await page.keyboard.press("Tab");
    assert.ok(await focusInDialog(), "Tab stays inside the dialog");
  }
  await page.keyboard.press("Escape");
  await dialog.waitFor({ state: "detached" });
  await page.waitForFunction(() => document.activeElement?.textContent === "Request Payment");
  assert.ok(await triggerFocused(), "Escape returns focus to the triggering button");
  assert.equal(await page.getByText("3", { exact: true }).count(), 0, "cancel skips the rest of the sequence");

  await trigger.click();
  await dialog.waitFor();
  const phone = dialog.getByLabel(/^MoMo Number/);
  assert.equal(await phone.inputValue(), "0244123456");
  assert.equal(await dialog.getByLabel(/^Amount/).inputValue(), "500");
  await phone.fill("0000");
  await dialog.getByRole("button", { name: "Send Request" }).click();
  await dialog.getByText("Not a MoMo number").waitFor();
  assert.ok(await dialog.isVisible(), "a 4xx keeps the dialog open");
  await phone.fill("0551234567");
  await dialog.getByRole("button", { name: "Send Request" }).click();
  await dialog.waitFor({ state: "detached" });
  await page.getByText("3", { exact: true }).waitFor();
  await page.waitForFunction(() => document.activeElement?.textContent === "Request Payment");
  assert.deepEqual(momoBodies, [null, null, { phone: "0000", amount: 50000 }, { phone: "0551234567", amount: 50000 }]);

  const beforePlain = requests.length;
  const plain = await page.evaluate(() => window.uiTest.callAction("sales.plain"));
  assert.equal(plain.data.saved, true);
  assert.ok(plain.ui, "a plain ui field remains data");
  assert.equal(requests.length, beforePlain + 1, "a plain action reloads the current view");
  await page.evaluate(() => window.uiTest.queryClient.setQueryData(["record", "sales.invoice", "01j"], { id: "01j" }));
  await page.evaluate(() =>
    window.uiTest.executeInstructions(
      [
        { type: "navigate", path: "/sales/invoices/01j" },
        { type: "reload", resource: "sales.invoice", record_id: "01j" },
      ],
      "response",
    ),
  );
  await page.getByRole("heading", { name: "Invoice 01j", exact: true }).waitFor();
  assert.equal(
    requests.filter((path) => path === "/record/01j").length,
    1,
    "reload waits for navigation and targets the new form",
  );
  await page.evaluate(() => window.uiTest.modalManager.open({ resource: "sales.invoice", recordId: "modal" }));
  await page.getByRole("heading", { name: "Invoice modal", exact: true }).waitFor();
  assert.equal(await page.evaluate(() => window.uiTest.currentExecutionContext().recordId), "modal");
  const beforeClose = requests.filter((path) => path === "/record/01j").length;
  await page.evaluate(() =>
    window.uiTest.executeInstructions(
      [{ type: "close_modal" }, { type: "reload", resource: "sales.invoice", record_id: "01j" }],
      "response",
    ),
  );
  assert.equal(
    requests.filter((path) => path === "/record/01j").length,
    beforeClose + 1,
    "reload after close targets the underlying form",
  );
  await page.evaluate(() =>
    window.uiTest.executeInstructions(
      [
        { type: "navigate", path: "/ignored" },
        { type: "set_badge", target: "/count", count: 9 },
      ],
      "push",
    ),
  );
  await page.getByText("9", { exact: true }).waitFor();
  assert.ok(page.url().endsWith("/_m/sales/invoices/01j"));
  assert.deepEqual(errors, []);
  console.log(
    "UI instruction browser checks passed: envelopes, header/menu/bulk actions, open_dialog focus and errors, badges, navigation, modal context and push filtering",
  );
} finally {
  await browser?.close();
  await server?.close();
  await rm(temp, { recursive: true, force: true });
}
