import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import tailwindcss from "@tailwindcss/vite";
import { chromium } from "playwright";
import { createServer } from "vite";

const repo = resolve(import.meta.dirname, "../../../..");
const temp = await mkdtemp(resolve(tmpdir(), "goerp-render-view-"));
let browser;
let server;

try {
  await symlink(resolve(repo, "shell/node_modules"), resolve(temp, "node_modules"), "dir");
  const fixture = JSON.parse(
    await readFile(resolve(repo, "modules/demo/frontend/src/api/generated.testing.json"), "utf8"),
  );
  await writeFile(
    resolve(temp, "index.html"),
    '<html><body><script type="module" src="/fixture.js"></script></body></html>',
  );
  await writeFile(
    resolve(temp, "fixture.js"),
    `
import { cleanup, configureViewTests, renderView } from '@goerp/shell-app/testing';
import { defineModule } from '@goerp/sdk/module';
import ${JSON.stringify(resolve(repo, "shell/packages/app/src/design/tokens.css"))};
configureViewTests({ fixture: ${JSON.stringify(fixture)}, module: defineModule({name:'demo'}) });
const item = {id:'item-1',name:'Browser item',created_at:'2026-10-14T00:00:00Z',updated_at:'2026-10-14T00:00:00Z'};
window.showView = async (mode) => {
  cleanup();
  const result = await renderView(mode === 'list' ? 'demo.demo_item_list' : 'demo.demo_item_form', {
    ...(mode === 'record' ? {recordId:'item-1'} : {}),
    api: {listItems:async()=>({data:[item],meta:{cursor:null,hasMore:false}}),getItem:async()=>item},
    apiMocks:{'GET /_meta/saved-filters':{data:[]},'GET /_meta/scheduled-activities':{data:[]},'GET /_meta/activity-types':{data:[]},'GET /_meta/activity':{data:[],meta:{cursor:null,has_more:false}},'GET /_meta/activity/followers':{data:[],meta:{following:false}}},
  });
  return result.container.textContent;
};
window.ready = true;
`,
  );
  server = await createServer({
    configFile: false,
    root: temp,
    plugins: [tailwindcss()],
    server: { host: "127.0.0.1", port: 0, fs: { allow: [temp, repo] } },
    optimizeDeps: { exclude: ["@goerp/shell-app/testing", "@duckdb/duckdb-wasm", "maplibre-gl"] },
  });
  await server.listen();
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(server.resolvedUrls.local[0]);
  await page.waitForFunction(() => window.ready);
  await page.evaluate(() => window.showView("list"));
  assert.equal(await page.getByText("Browser item").count(), 1);
  await page.evaluate(() => window.showView("record"));
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await page.getByRole("textbox", { name: /Name/ }).waitFor();
  assert.equal(await page.getByRole("textbox", { name: /Name/ }).inputValue(), "Browser item");
  assert.equal(
    await page.getByRole("textbox", { name: /Name/ }).evaluate((input) => input === document.activeElement),
    true,
  );
  await page.evaluate(() => window.showView("create"));
  assert.equal(await page.getByRole("textbox", { name: /Name/ }).inputValue(), "");
  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(await page.getByRole("textbox", { name: /Name/ }).isVisible(), true);
  assert.deepEqual(errors, []);
  await page.screenshot({ path: "/tmp/goerp-1706-create-form.png" });
  console.log("renderView Chromium checks passed: rows, record form, edit focus, empty form and mobile visibility");
} finally {
  await browser?.close();
  await server?.close();
  await rm(temp, { recursive: true, force: true });
}
