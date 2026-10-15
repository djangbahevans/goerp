import assert from "node:assert/strict";
import { mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { chromium } from "playwright";
import { createServer } from "vite";
import { sharedPackages } from "../src/dev-server/shared-packages.ts";

const repo = resolve(import.meta.dirname, "../../../..");
const temp = await mkdtemp(resolve(tmpdir(), "goerp-navigation-"));
let browser;
let server;

const schema = {
  engine_version: "test",
  schema_hash: "navigation",
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
          fields: [],
          enabled_ops: ["list", "get"],
          shareable: false,
        },
      },
      routes: [
        {
          method: "GET",
          path: "/contacts",
          model: "contacts.contact",
          crud_action: "list",
          engine_native: true,
          permissions: [],
          response_is_list: true,
        },
        {
          method: "GET",
          path: "/contacts/{id}",
          model: "contacts.contact",
          crud_action: "get",
          engine_native: true,
          permissions: [],
          response_is_list: false,
          view: "contact_form",
        },
      ],
      views: [{ name: "contact_form", type: "form", resource: "contacts.contact", label: "Contact", sections: [] }],
      navigation: [],
      permissions: [],
      frontend: null,
      view_extensions: [],
      view_extension_definitions: [],
      load_order: 0,
      public_config: {},
      notification_types: [],
      error_codes: [],
    },
  },
};

try {
  await symlink(resolve(repo, "shell/node_modules"), resolve(temp, "node_modules"), "dir");
  await writeFile(
    resolve(temp, "index.html"),
    '<head><!--goerp-import-map--><style>body{font:16px system-ui;margin:16px}output{display:block;wrap-anywhere;margin:12px 0}button,a{margin:4px}</style></head><body><div id="root"></div><script type="module" src="/fixture.tsx"></script></body>',
  );
  await writeFile(
    resolve(temp, "fixture.tsx"),
    `
import React, { useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { AuthContext, PermissionContext, createPermissionContextValue } from '@goerp/sdk/auth';
import { ModuleNavigationProvider } from '@goerp/sdk/react';
import { useNavigate, useModuleLink, useResourceRegistry, useCurrentView } from '@goerp/sdk/nav';
import { componentRegistry, ViewRegistryProviderForTenant, useRefreshViewRegistry } from '@goerp/sdk/schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRootRoute, createRoute, createRouter, createBrowserHistory, RouterProvider, Outlet, useLocation } from '@tanstack/react-router';
import { useModuleNavigate } from ${JSON.stringify(resolve(repo, "shell/packages/app/src/router/use-module-navigate.ts"))};
import { useCommandRunner } from ${JSON.stringify(resolve(repo, "shell/packages/app/src/chrome/use-command-runner.ts"))};
import { useRecentCommands } from ${JSON.stringify(resolve(repo, "shell/packages/app/src/chrome/use-recent-commands.ts"))};
import { ViewDispatch } from ${JSON.stringify(resolve(repo, "shell/packages/app/src/renderers/view-dispatch.tsx"))};

function ContextProbe({ embedded }) {
  const view = useCurrentView();
  return <output data-testid={embedded ? 'embedded-view' : 'page-view'}>{JSON.stringify({ ...view, params: view?.params.toString() })}</output>;
}
componentRegistry.register('ContextProbe', ContextProbe);
const view = { name: 'dashboard', type: 'custom', label: 'Dashboard', resource: '', component: 'ContextProbe' };
function Page() {
  const search = useLocation({ select: location => location.searchStr });
  const params = useMemo(() => new URLSearchParams(search), [search]);
  return <><h1>Module page</h1><ViewDispatch view={view} module="contacts" params={params} /><ViewDispatch view={{ ...view, name: 'embedded_dashboard' }} module="contacts" embedded recordId="parent" params={params} /></>;
}
function Chrome() {
  const navigate = useNavigate();
  const link = useModuleLink('contacts.contact', 'c1');
  const registry = useResourceRegistry();
  const current = useCurrentView();
  const refresh = useRefreshViewRegistry();
  const run = useCommandRunner();
  const recent = useRecentCommands();
  return <><output data-testid="chrome-view">{String(current)}</output>
    <output data-testid="registry">{registry.get('contacts.contact')?.defaultFormView ?? 'unavailable'}</output>
    <a href={link.href ?? undefined}>Record href</a>
    <button disabled={!link.isAvailable} onClick={link.navigate}>Open record</button>
    <button onClick={() => navigate('/contacts?tab=notes&stage=lead')}>Module</button>
    <button onClick={() => navigate('/contacts/new', { replace: true })}>Replace</button>
    <button onClick={() => navigate('/settings/profile', { shell: true })}>Profile</button>
    <button onClick={() => run({ id: 'module', label: 'Module command', action: ctx => ctx.navigate('/contacts/c2?tab=notes') })}>Command</button>
    <button onClick={refresh}>Reload schema</button>
    {recent.map(command => <button key={command.id} onClick={() => run(command)}>Replay {command.label}</button>)}
    <Outlet />
  </>;
}
function Root() {
  const navigate = useModuleNavigate();
  return <ModuleNavigationProvider navigate={navigate}><Chrome /></ModuleNavigationProvider>;
}
const root = createRootRoute({ component: Root });
const routes = [
  createRoute({ getParentRoute: () => root, path: '/', component: () => <h1>Home</h1> }),
  createRoute({ getParentRoute: () => root, path: '/_m/$', component: Page }),
  createRoute({ getParentRoute: () => root, path: '/settings/profile', component: () => <h1>Profile</h1> }),
];
const router = createRouter({ routeTree: root.addChildren(routes), history: createBrowserHistory() });
const queryClient = new QueryClient();
const auth = { user: { id: 'u1' }, tenant: { id: 't1' } };
function App() {
  const [enabled, setEnabled] = useState(true);
  window.setModuleEnabled = setEnabled;
  const permissions = useMemo(() => createPermissionContextValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set(enabled ? ['contacts'] : []) }), [enabled]);
  return <QueryClientProvider client={queryClient}><AuthContext value={auth}><PermissionContext value={permissions}><ViewRegistryProviderForTenant isAuthenticated tenantId="t1"><RouterProvider router={router} /></ViewRegistryProviderForTenant></PermissionContext></AuthContext></QueryClientProvider>;
}
createRoot(document.getElementById('root')).render(<App />);
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
  const page = await browser.newPage({ viewport: { width: 1100, height: 800 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  let releaseSchema;
  const schemaReady = new Promise((accept) => {
    releaseSchema = accept;
  });
  await page.route("**/_meta/schema", async (route) => {
    await schemaReady;
    await route.fulfill({ json: schema });
  });
  await page.goto(server.resolvedUrls.local[0]);
  await page.getByRole("heading", { name: "Home" }).waitFor();
  assert.equal(await page.getByRole("button", { name: "Open record", exact: true }).isDisabled(), true);
  assert.equal(await page.getByTestId("registry").textContent(), "unavailable");
  releaseSchema();
  await page.waitForFunction(() => document.querySelector('[data-testid="registry"]').textContent === "contact_form");
  assert.equal(await page.getByRole("link", { name: "Record href" }).getAttribute("href"), "/_m/contacts/c1");
  await page.getByRole("button", { name: "Open record", exact: true }).click();
  await page.waitForURL("**/_m/contacts/c1");
  await page.getByRole("heading", { name: "Module page" }).waitFor();
  await page.getByRole("button", { name: "Module", exact: true }).click();
  await page.waitForURL("**/_m/contacts?tab=notes&stage=lead");
  const current = JSON.parse(await page.getByTestId("page-view").textContent());
  assert.deepEqual(current, {
    viewName: "dashboard",
    moduleName: "contacts",
    resource: null,
    recordId: null,
    embedded: false,
    params: "tab=notes&stage=lead",
  });
  const embedded = JSON.parse(await page.getByTestId("embedded-view").textContent());
  assert.equal(embedded.viewName, "embedded_dashboard");
  assert.equal(embedded.recordId, "parent");
  assert.equal(embedded.embedded, true);
  assert.equal(embedded.params, "");
  assert.equal(await page.getByTestId("chrome-view").textContent(), "null");
  const historyLength = await page.evaluate(() => history.length);
  await page.getByRole("button", { name: "Replace", exact: true }).click();
  await page.waitForURL("**/_m/contacts/new");
  assert.equal(await page.evaluate(() => history.length), historyLength);
  await page.getByRole("button", { name: "Profile", exact: true }).click();
  await page.waitForURL("**/settings/profile");
  await page.getByRole("heading", { name: "Profile" }).waitFor();
  await page.getByRole("button", { name: "Command", exact: true }).click();
  await page.waitForURL("**/_m/contacts/c2?tab=notes");
  await page.getByRole("button", { name: "Replay /settings/profile", exact: true }).click();
  await page.waitForURL("**/settings/profile");
  await page.getByRole("button", { name: "Replay /_m/contacts/new", exact: true }).click();
  await page.waitForURL("**/_m/contacts/new");
  await page.evaluate(() => window.setModuleEnabled(false));
  await page.waitForFunction(() => document.querySelector("button").disabled);
  assert.equal(await page.getByRole("link", { name: "Record href" }).count(), 0);
  await page.evaluate(() => window.setModuleEnabled(true));
  schema.modules.contacts.routes[1].path = "/contacts/people/{id}";
  await page.getByRole("button", { name: "Reload schema", exact: true }).click();
  await page.waitForFunction(() => document.querySelector("a").getAttribute("href") === "/_m/contacts/people/c1");
  await page.getByRole("button", { name: "Open record", exact: true }).click();
  await page.waitForURL("**/_m/contacts/people/c1");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Module", exact: true }).focus();
  await page.keyboard.press("Enter");
  await page.waitForURL("**/_m/contacts?tab=notes&stage=lead");
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  const screenshot = resolve(tmpdir(), "goerp-1705-navigation.png");
  await page.screenshot({ path: screenshot });
  assert.deepEqual(errors, []);
  console.log(
    `PASS navigation hooks: browser routing, commands, replay, contexts, availability, schema reload and keyboard navigation. Screenshot: ${screenshot}`,
  );
} finally {
  await browser?.close();
  await server?.close();
  await rm(temp, { recursive: true, force: true });
}
