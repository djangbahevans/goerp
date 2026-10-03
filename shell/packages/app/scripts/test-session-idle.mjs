import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { promisify } from "node:util";
import { chromium } from "playwright";

const run = promisify(execFile);
const repo = resolve(import.meta.dirname, "../../../..");
const slug = `idle${Date.now().toString(36)}`;
const email = `admin@${slug}.test`;
const password = `Idle-${randomUUID()}`;
const shots = await mkdtemp(resolve(tmpdir(), "goerp-session-idle-"));

async function sql(statement) {
  const { stdout } = await run(
    "docker",
    [
      "compose",
      "-f",
      "compose.dev.yml",
      "exec",
      "-T",
      "postgres",
      "psql",
      "-U",
      "goerp",
      "-d",
      "goerp_dev",
      "-v",
      "ON_ERROR_STOP=1",
      "-qAt",
      "-c",
      statement,
    ],
    { cwd: repo },
  );
  return stdout.trim();
}

async function schedule(page) {
  await page.evaluate(async () => {
    const { tokenRefreshScheduler } = await import("@goerp/sdk/auth");
    tokenRefreshScheduler.schedule(0.25);
  });
  await page.waitForTimeout(400);
}

async function request(page, background) {
  return page.evaluate(async (background) => {
    const { apiClient } = await import("@goerp/sdk");
    try {
      await apiClient.get("/_notif/count", { background });
      return 200;
    } catch (err) {
      return err.httpStatus;
    }
  }, background);
}

async function userRequest(page) {
  await page.evaluate(async () => {
    const { apiClient } = await import("@goerp/sdk");
    window.idleRequestResult = null;
    document.addEventListener(
      "keydown",
      async () => {
        try {
          await apiClient.get("/_notif/count");
          window.idleRequestResult = 200;
        } catch (err) {
          window.idleRequestResult = err.httpStatus;
        }
      },
      { once: true },
    );
  });
  await page.keyboard.press("Shift");
  await page.waitForFunction(() => window.idleRequestResult !== null);
  return page.evaluate(() => window.idleRequestResult);
}

let browser;
try {
  try {
    await run(resolve(repo, ".agents/skills/run-goerp/bootstrap-tenant.sh"), [slug, email, password], { cwd: repo });
  } catch (err) {
    throw new Error(`Could not provision the idle-session fixture: ${err.stderr}`);
  }
  await sql(`INSERT INTO system.tenant_config_overrides (tenant_id, key, value)
    SELECT id, 'auth.session_idle_timeout', '15' FROM system.tenants WHERE slug = '${slug}'
    ON CONFLICT (tenant_id, key) DO UPDATE SET value = EXCLUDED.value`);

  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  await context.addInitScript(() => {
    window.idleSockets = [];
    const NativeWebSocket = window.WebSocket;
    window.WebSocket = class extends NativeWebSocket {
      constructor(...args) {
        super(...args);
        window.idleSockets.push(this);
      }
    };
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (err) => errors.push(err.message));
  let refreshes = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/auth/refresh") refreshes += 1;
  });
  await page.goto(`http://${slug}.localhost:5173/auth/login?redirect=%2Fsettings%2Fsecurity`);
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("heading", { name: "Security", exact: true }).waitFor();

  await schedule(page);
  assert.equal(refreshes, 0, "idle mount refreshed");
  await page.evaluate(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "a" })));
  await schedule(page);
  assert.equal(refreshes, 0, "synthetic input refreshed");

  await page.keyboard.press("Shift");
  await schedule(page);
  assert.equal(refreshes, 1, "trusted keyboard input did not refresh");
  await schedule(page);
  assert.equal(refreshes, 1, "activity was reused after refresh");
  await page.mouse.move(1100, 750);
  await schedule(page);
  assert.equal(refreshes, 2, "trusted pointer input did not refresh");
  console.log("PASS idle and synthetic input do not refresh; trusted keyboard/pointer input refreshes once per cycle");

  await context.clearCookies({ name: "__Host-access_token" });
  assert.equal(await request(page, true), 401);
  assert.equal(await request(page, false), 401);
  await page.evaluate(() => window.idleSockets.at(-1)?.close());
  await page.waitForTimeout(1500);
  assert.equal(refreshes, 2, "idle background traffic refreshed");
  assert.equal(await page.getByRole("alertdialog").count(), 0);
  assert.equal(await userRequest(page), 200);
  assert.equal(refreshes, 3, "the next user request did not recover within the idle window");
  console.log(
    "PASS notification/background requests and WebSocket reconnect do not refresh; user input recovers a live session",
  );

  const access = (await context.cookies()).find((cookie) => cookie.name === "__Host-access_token");
  assert(access);
  const { sid } = JSON.parse(Buffer.from(access.value.split(".")[1], "base64url"));
  assert.match(sid, /^[0-9a-f-]{36}$/);
  assert.equal(
    await sql(`UPDATE system.sessions SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = '${sid}' RETURNING id`),
    sid,
  );
  await context.clearCookies({ name: "__Host-access_token" });
  assert.equal(await request(page, true), 401);
  assert.equal(refreshes, 3);
  assert.equal(await userRequest(page), 401);
  await page.getByRole("alertdialog").waitFor();
  await page.waitForFunction(() => {
    const dialog = document.querySelector('[role="alertdialog"]');
    return dialog && Number(getComputedStyle(dialog).opacity) === 1;
  });
  assert.equal(await page.evaluate(() => document.activeElement?.textContent?.trim()), "Sign in again");
  await page.screenshot({ path: resolve(shots, "expired-session-desktop.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: resolve(shots, "expired-session-mobile.png") });
  await page.getByRole("button", { name: "Sign in again", exact: true }).click();
  await page.getByRole("heading", { name: "Sign in", exact: true }).waitFor();
  assert(new URL(page.url()).searchParams.get("redirect") === "/settings/security");
  assert.deepEqual(errors, []);
  console.log(`PASS expired idle session enters the login flow and preserves the intended page\nScreenshots: ${shots}`);
} finally {
  await browser?.close();
}
