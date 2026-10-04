import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createHmac } from "node:crypto";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { promisify } from "node:util";
import { chromium } from "playwright";

const run = promisify(execFile);
const repo = resolve(import.meta.dirname, "../../../..");
const slug = `pwd${Date.now().toString(36)}`;
const secondSlug = `${slug}b`;
const email = `admin@${slug}.test`;
const initialPassword = "Pwd1350-Start!";
const newPassword = "Pwd1350-Long-Passphrase!";
const shots = await mkdtemp(resolve(tmpdir(), "goerp-password-policy-"));
console.log(`Screenshots: ${shots}`);
const origin = `http://${slug}.localhost:5173`;
const onward = "/settings/appearance";
const errors = [];

function totp(secret) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  const bits = [...secret.replace(/=+$/, "").toUpperCase()]
    .map((c) => alphabet.indexOf(c).toString(2).padStart(5, "0"))
    .join("");
  const key = Buffer.from(bits.match(/.{8}/g).map((byte) => Number.parseInt(byte, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30_000)));
  const digest = createHmac("sha1", key).update(counter).digest();
  const offset = digest.at(-1) & 15;
  return ((digest.readUInt32BE(offset) & 0x7fffffff) % 1_000_000).toString().padStart(6, "0");
}

async function provision(tenant, password) {
  try {
    await run(resolve(repo, ".agents/skills/run-goerp/bootstrap-tenant.sh"), [tenant, email, password], { cwd: repo });
  } catch (err) {
    throw new Error(`Could not provision the password-policy fixture: ${err.stderr}`);
  }
}

async function api(page, path, method = "GET", body) {
  return page.evaluate(
    async ({ path, method, body }) => {
      const response = await fetch(path, {
        method,
        headers: { "Content-Type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      return { status: response.status, body: await response.json() };
    },
    { path, method, body },
  );
}

async function login(page, password, host = origin, destination = "/settings/profile") {
  await page.goto(`${host}/auth/login?${new URLSearchParams({ redirect: destination })}`);
  await page.getByLabel("Email", { exact: true }).fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
}

async function requirePassword(page, minimum, graceDays) {
  const response = await api(page, "/admin/settings", "PATCH", {
    security: { password_policy: { min_length: minimum, enforcement: "require", grace_days: graceDays } },
  });
  assert.equal(response.status, 200, JSON.stringify(response.body));
}

let browser;
try {
  await provision(slug, initialPassword);
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on("pageerror", (err) => errors.push(err.message));

  await login(page, initialPassword);
  await page.getByRole("heading", { name: "Profile", exact: true }).waitFor();
  await requirePassword(page, 18, 30);
  await api(page, "/auth/logout", "POST");
  await login(page, initialPassword);
  await page.getByRole("heading", { name: "Profile", exact: true }).waitFor();
  const deadline = (await api(page, "/auth/me")).body;
  assert.equal(deadline.user.password_min_length, 18);
  await page.getByText(/You'll need to change it by/).waitFor();
  const stored = await page.evaluate(() => JSON.parse(sessionStorage.getItem("goerp-password-update-notice")));
  assert.equal(stored.recommended, true);
  assert(Number.isFinite(Date.parse(stored.deadline)));
  await page.reload();
  await page.getByText(/You'll need to change it by/).waitFor();
  assert.deepEqual(
    await page.evaluate(() => JSON.parse(sessionStorage.getItem("goerp-password-update-notice"))),
    stored,
  );
  console.log("PASS password deadline survives navigation and reload");

  await requirePassword(page, 18, 0);
  await api(page, "/auth/logout", "POST");
  await login(page, initialPassword, "http://localhost:5173", onward);
  await page.getByRole("heading", { name: /Change your password to keep using/ }).waitFor();
  assert.equal(new URL(page.url()).hostname, `${slug}.localhost`);
  assert.equal(new URL(page.url()).pathname, "/settings/profile");
  assert.equal(new URL(page.url()).searchParams.get("redirect"), onward);
  assert.equal(new URL(page.url()).hash, "#change-password");
  assert.equal(await page.getByRole("navigation").count(), 0);
  assert.equal(await page.getByLabel("Full name").count(), 0);
  await page.getByText("It needs at least 18 characters.").waitFor();
  await page.waitForFunction(() => document.activeElement?.getAttribute("autocomplete") === "current-password");
  assert.equal(await page.getByLabel("New password", { exact: true }).getAttribute("minlength"), "18");
  await page.keyboard.press("Control+k");
  assert.equal(await page.getByRole("dialog").count(), 0);
  await page.screenshot({ path: resolve(shots, "restricted-desktop.png") });
  await page.setViewportSize({ width: 390, height: 650 });
  await page.getByRole("button", { name: "Sign out", exact: true }).scrollIntoViewIfNeeded();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await page.screenshot({ path: resolve(shots, "restricted-mobile.png"), fullPage: true });
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await page.getByRole("heading", { name: "Sign in", exact: true }).waitFor();
  console.log("PASS shared-domain handoff reaches restricted mode; mobile scrolling, focus, shortcuts and sign-out");

  await login(page, initialPassword, origin, onward);
  await page.getByRole("heading", { name: /Change your password to keep using/ }).waitFor();
  await api(page, "/auth/logout", "POST");
  await page.evaluate(async () => {
    const { authMachine } = await import("@goerp/sdk/auth");
    authMachine.transition({ type: "session_expired" });
  });
  await page.getByRole("alertdialog").waitFor();
  await page.getByRole("button", { name: "Sign in again", exact: true }).click();
  await page.getByRole("heading", { name: "Sign in", exact: true }).waitFor();
  assert.equal(new URL(page.url()).searchParams.get("redirect"), onward);
  await login(page, initialPassword, origin, onward);
  await page.getByRole("heading", { name: /Change your password to keep using/ }).waitFor();
  console.log("PASS an expired restricted session reaches sign-in again and keeps the original destination");
  await page.getByLabel("Current password").fill(initialPassword);
  await page.getByLabel("New password", { exact: true }).fill(newPassword);
  await page.getByLabel("Confirm new password").fill(newPassword);
  await page.getByRole("button", { name: "Change password", exact: true }).click();
  await page.getByRole("heading", { name: "Appearance", exact: true }).waitFor();
  assert.equal(new URL(page.url()).pathname, onward);
  assert.equal((await api(page, "/auth/me")).body.user.password_change_required, false);
  assert.equal(await page.evaluate(() => sessionStorage.getItem("goerp-password-update-notice")), null);
  console.log("PASS tenant-host login, password change, session reload and intended return navigation");

  await page.goto(`${origin}/settings/profile`);
  await page.getByLabel("Full name", { exact: true }).fill("Password Policy Tester");
  await page.getByLabel("Phone", { exact: true }).fill("+233201234567");
  await page.getByLabel("Job title").fill("Consultant");
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await page.getByText("Profile updated.", { exact: true }).waitFor();
  await page.reload();
  assert.equal(await page.getByLabel("Phone", { exact: true }).inputValue(), "+233201234567");
  assert.equal(await page.getByLabel("Job title").inputValue(), "Consultant");

  await provision(secondSlug, newPassword);
  const second = await context.newPage();
  second.on("pageerror", (err) => errors.push(err.message));
  await login(second, newPassword, `http://${secondSlug}.localhost:5173`);
  await second.getByRole("heading", { name: "Profile", exact: true }).waitFor();
  assert.equal(await second.getByLabel("Phone", { exact: true }).inputValue(), "");
  assert.equal(await second.getByLabel("Job title").inputValue(), "");
  await second.getByLabel("Job title").fill("Advisor");
  await second.getByRole("button", { name: "Save changes", exact: true }).click();
  await second.getByText("Profile updated.", { exact: true }).waitFor();
  await requirePassword(second, 20, 30);
  await page.reload();
  assert.equal(await page.getByLabel("Job title").inputValue(), "Consultant");
  assert.equal(await page.getByLabel("New password", { exact: true }).getAttribute("minlength"), "20");
  console.log("PASS phone/title stay tenant-scoped and the password form uses the account's combined minimum");
  await requirePassword(second, 18, 30);
  const shortPassword = "Pwd1350-Mfa-Short!";
  assert.equal(
    (
      await api(page, "/auth/me/change-password", "POST", {
        current_password: newPassword,
        new_password: shortPassword,
      })
    ).status,
    200,
  );
  const enrollment = await api(page, "/auth/mfa/enroll/totp", "POST", {});
  assert.equal(enrollment.status, 200, JSON.stringify(enrollment.body));
  assert.equal(
    (
      await api(page, "/auth/mfa/enroll/totp/confirm", "POST", {
        enrollment_id: enrollment.body.enrollment_id,
        code: totp(enrollment.body.secret),
      })
    ).status,
    200,
  );
  await requirePassword(page, 20, 0);
  await api(page, "/auth/logout", "POST");
  await login(page, shortPassword, origin, onward);
  await page.getByRole("heading", { name: "Enter your verification code", exact: true }).waitFor();
  await page.waitForTimeout(30_000 - (Date.now() % 30_000) + 1000);
  await page.getByLabel("Verification code").fill(totp(enrollment.body.secret));
  await page.getByRole("heading", { name: /Change your password to keep using/ }).waitFor();
  assert.equal(new URL(page.url()).searchParams.get("redirect"), onward);
  await page.getByText("It needs at least 20 characters.").waitFor();
  await page.getByLabel("Current password").fill(shortPassword);
  await page.getByLabel("New password", { exact: true }).fill(newPassword);
  await page.getByLabel("Confirm new password").fill(newPassword);
  await page.getByRole("button", { name: "Change password", exact: true }).click();
  await page.getByRole("heading", { name: "Appearance", exact: true }).waitFor();
  console.log("PASS MFA verification enters restricted mode and password completion restores the intended page");
  assert.deepEqual(errors, []);
  console.log(`Screenshots: ${shots}`);
} finally {
  await browser?.close();
}
