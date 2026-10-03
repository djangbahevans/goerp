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
const shots = await mkdtemp(resolve(tmpdir(), "goerp-passkeys-"));
const suffix = Date.now().toString(36);
const slug = `passkey${suffix}`;
const setupSlug = `passkeysetup${suffix}`;
const password = `Passkey-${randomUUID()}`;
const email = `admin@${slug}.test`;
const setupEmail = `admin@${setupSlug}.test`;
const origin = `http://${slug}.localhost:5173`;
const setupOrigin = `http://${setupSlug}.localhost:5173`;

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
      "-At",
      "-c",
      statement,
    ],
    { cwd: repo },
  );
  return stdout.trim();
}

async function provision(tenant, admin) {
  try {
    await run(resolve(repo, ".agents/skills/run-goerp/bootstrap-tenant.sh"), [tenant, admin, password], { cwd: repo });
  } catch (err) {
    throw new Error(`Could not provision the passkey fixture: ${err.stderr}`);
  }
  console.log(`Provisioned ${tenant}`);
}

async function login(page, base, admin) {
  await page.goto(`${base}/auth/login?redirect=%2Fsettings%2Fsecurity`);
  await page.getByLabel("Email").fill(admin);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("heading", { name: "Sign in", exact: true }).waitFor({ state: "detached" });
}

async function authenticator(context, page) {
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: {
      protocol: "ctap2",
      transport: "internal",
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  });
  return {
    cdp,
    authenticatorId,
    presence: (enabled) => cdp.send("WebAuthn.setAutomaticPresenceSimulation", { authenticatorId, enabled }),
  };
}

async function ageAssurance(context, page) {
  const access = (await context.cookies()).find((cookie) => cookie.name === "__Host-access_token");
  assert(access, "access token missing");
  const { sid } = JSON.parse(Buffer.from(access.value.split(".")[1], "base64url"));
  assert.match(sid, /^[0-9a-f-]{36}$/);
  const result = await sql(
    `UPDATE system.sessions SET mfa_verified_at = NOW() - INTERVAL '2 days' WHERE id = '${sid}' RETURNING id`,
  );
  assert(result.startsWith(sid), "the browser's session was not aged");
  assert.equal(
    await page.evaluate(
      async () =>
        (
          await fetch("/auth/refresh", {
            method: "POST",
            credentials: "include",
            headers: { "Content-Type": "application/json" },
            body: "{}",
          })
        ).status,
    ),
    200,
  );
}

const errors = [];
let browser;
let debugPage;
try {
  await provision(slug, email);
  await provision(setupSlug, setupEmail);
  await sql(`INSERT INTO system.tenant_config_overrides (tenant_id, key, value)
    SELECT id, 'mfa.enforcement_mode', 'required' FROM system.tenants WHERE slug = '${setupSlug}'
    ON CONFLICT (tenant_id, key) DO UPDATE SET value = EXCLUDED.value`);

  browser = await chromium.launch({ headless: true });
  const unsupported = await browser.newContext();
  await unsupported.addInitScript(() => {
    window.PublicKeyCredential = undefined;
  });
  const unsupportedPage = await unsupported.newPage();
  await login(unsupportedPage, origin, email);
  await unsupportedPage.getByRole("heading", { name: "Security", exact: true }).waitFor();
  assert.equal(await unsupportedPage.getByRole("button", { name: "Add passkey" }).count(), 0);
  assert.equal(await unsupportedPage.getByRole("button", { name: "Add authenticator app" }).count(), 1);
  await unsupported.close();

  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  debugPage = page;
  await page.addInitScript(() => {
    window.passkeyBrowserErrors = [];
    window.passkeyBrowserCalls = [];
    for (const method of ["create", "get"]) {
      const operation = navigator.credentials[method].bind(navigator.credentials);
      navigator.credentials[method] = async (options) => {
        window.passkeyBrowserCalls.push(method);
        try {
          return await operation(options);
        } catch (err) {
          window.passkeyBrowserErrors.push({ name: err.name, message: err.message });
          throw err;
        }
      };
    }
  });
  page.on("pageerror", (err) => errors.push(err.message));
  const requests = [];
  page.on("request", (request) => {
    if (request.url().includes("/auth/mfa/")) requests.push(new URL(request.url()).pathname);
  });
  const virtual = await authenticator(context, page);
  await login(page, origin, email);
  await page.getByRole("heading", { name: "Security", exact: true }).waitFor();

  const add = page.getByRole("button", { name: "Add passkey", exact: true });
  await add.click();
  const sheet = page.getByRole("dialog", { name: "Add passkey" });
  await sheet.getByRole("heading", { name: "Add passkey" }).waitFor();
  await sheet.getByLabel("Name").fill("Laptop");
  await virtual.presence(false);
  await sheet.getByRole("button", { name: "Create passkey" }).click();
  await page.waitForFunction(() => window.passkeyBrowserCalls.includes("create"));
  await sheet.getByRole("button", { name: "Cancel passkey" }).click();
  await sheet.getByRole("button", { name: "Create passkey" }).waitFor({ state: "visible" });
  await page.waitForFunction(() => document.activeElement?.textContent === "Create passkey");
  assert.equal(requests.filter((path) => path.endsWith("webauthn/confirm")).length, 0);
  assert.equal(await sheet.getByRole("alert").count(), 0);
  assert((await page.evaluate(() => window.passkeyBrowserErrors)).some((err) => err.name === "AbortError"));

  await virtual.presence(true);
  await sheet.getByRole("button", { name: "Create passkey" }).click();
  await sheet.getByRole("list", { name: "Recovery codes" }).waitFor();
  await page.keyboard.press("Escape");
  assert(await sheet.isVisible(), "unsaved recovery codes were dismissed");
  await page.screenshot({ path: resolve(shots, "enrollment-desktop.png") });
  await page.setViewportSize({ width: 390, height: 360 });
  await sheet.getByText("I've saved these codes", { exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: resolve(shots, "enrollment-mobile.png") });
  assert(
    await sheet.evaluate((element) => {
      const body = element.lastElementChild;
      const rect = element.getBoundingClientRect();
      return (
        rect.left >= 0 &&
        rect.right <= innerWidth &&
        rect.bottom <= innerHeight &&
        body.scrollHeight > body.clientHeight
      );
    }),
    "mobile sheet does not fit or scroll",
  );
  await sheet.getByText("I've saved these codes", { exact: true }).click();
  assert(await sheet.getByLabel("I've saved these codes").isChecked());
  await sheet.getByRole("button", { name: "Done" }).click();
  await sheet.waitFor({ state: "detached" });
  await page.waitForFunction(() => document.activeElement?.textContent === "Add passkey");
  await page.getByRole("cell", { name: "Laptop", exact: true }).waitFor();
  const credentials = await virtual.cdp.send("WebAuthn.getCredentials", { authenticatorId: virtual.authenticatorId });
  assert.equal(credentials.credentials.length, 1);
  console.log("PASS enrollment, native cancellation, recovery acknowledgment, mobile scrolling and return focus");

  await page.setViewportSize({ width: 1280, height: 900 });
  assert.equal(
    await page.evaluate(async () => (await fetch("/auth/logout", { method: "POST", credentials: "include" })).status),
    200,
  );
  await login(page, origin, email);
  await page.getByRole("button", { name: "Use a passkey" }).waitFor();
  const verifies = requests.filter((path) => path === "/auth/mfa/verify").length;
  await virtual.presence(false);
  await page.getByRole("button", { name: "Use a passkey" }).click();
  await page.waitForFunction(() => window.passkeyBrowserCalls.includes("get"));
  await page.getByRole("button", { name: "Cancel passkey" }).click();
  await page.waitForFunction(() => document.activeElement?.textContent === "Use a passkey");
  assert.equal(requests.filter((path) => path === "/auth/mfa/verify").length, verifies);
  assert.equal(await page.getByRole("alert").count(), 0);
  assert(new URL(page.url()).pathname === "/auth/mfa");
  await virtual.presence(true);
  await page.getByRole("button", { name: "Use a passkey" }).click();
  await page.getByRole("heading", { name: "Security", exact: true }).waitFor();
  console.log("PASS native login assertion, cancelled challenge retry and session redirect");

  await ageAssurance(context, page);
  await page.getByRole("button", { name: "Add authenticator app" }).click();
  const totp = page.getByRole("dialog", { name: "Add authenticator app" });
  await totp.getByRole("button", { name: "Use a passkey" }).click();
  await totp.getByRole("img", { name: /QR code/ }).waitFor();
  assert(requests.includes("/auth/mfa/reverify/webauthn/options"));
  assert(requests.includes("/auth/mfa/reverify"));
  await totp.getByRole("button", { name: "Close", exact: true }).click();
  await totp.waitFor({ state: "detached" });
  console.log("PASS passkey step-up for authenticator enrollment with real expired assurance");

  await ageAssurance(context, page);
  await add.click();
  await sheet.getByLabel("Name").fill("Security key");
  await sheet.getByRole("button", { name: "Create passkey" }).click();
  await sheet.getByRole("button", { name: "Use a passkey" }).click();
  await sheet
    .getByText("This authenticator already has a passkey for this account. Use another device or security key.")
    .waitFor();
  // Clearing emulator credentials represents switching to an unenrolled authenticator.
  await virtual.cdp.send("WebAuthn.clearCredentials", { authenticatorId: virtual.authenticatorId });
  await sheet.getByRole("button", { name: "Create passkey" }).click();
  await sheet.waitFor({ state: "detached" });
  await page.getByRole("cell", { name: "Security key", exact: true }).waitFor();
  console.log("PASS passkey step-up and a fresh enrollment ceremony after expired assurance");

  const setupContext = await browser.newContext({ viewport: { width: 390, height: 844 } });
  const setupPage = await setupContext.newPage();
  debugPage = setupPage;
  setupPage.on("pageerror", (err) => errors.push(err.message));
  await authenticator(setupContext, setupPage);
  await login(setupPage, setupOrigin, setupEmail);
  await setupPage.getByRole("button", { name: "Use a passkey" }).click();
  await setupPage.getByLabel("Name").fill("First passkey");
  await setupPage.getByRole("button", { name: "Create passkey" }).click();
  await setupPage.getByRole("list", { name: "Recovery codes" }).waitFor();
  await setupPage.screenshot({ path: resolve(shots, "forced-enrollment-mobile.png") });
  await setupPage.getByText("I've saved these codes", { exact: true }).click();
  assert(await setupPage.getByLabel("I've saved these codes").isChecked());
  await setupPage.getByRole("button", { name: "Finish" }).click();
  await setupPage.getByRole("heading", { name: "Security", exact: true }).waitFor();
  await setupPage.getByRole("cell", { name: "First passkey", exact: true }).waitFor();
  assert.equal(
    await setupPage.evaluate(async () => (await (await fetch("/auth/me")).json()).user.mfa_setup_required),
    false,
  );
  assert.deepEqual(errors, []);
  console.log(`PASS required-MFA first-factor enrollment and session reload\nScreenshots: ${shots}`);
} catch (err) {
  if (debugPage) {
    console.error({
      url: debugPage.url(),
      alerts: await debugPage.getByRole("alert").allTextContents(),
      browserErrors: await debugPage.evaluate(() => window.passkeyBrowserErrors),
    });
    await debugPage.screenshot({ path: resolve(shots, "failure.png") });
    console.error(`Failure screenshot: ${shots}/failure.png`);
  }
  throw err;
} finally {
  await browser?.close();
}
