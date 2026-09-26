// Headless-Chromium REPL for the goerp shell app. Reads one command per line
// from stdin (a piped heredoc or tmux send-keys) and runs them in order.
//
//   node .claude/skills/run-goerp/driver.mjs <<'EOF'
//   open smoke
//   login admin@smoke.test Smoke-Pass-2026!
//   nav /activities
//   ss activities
//   errors
//   EOF
//
// Commands:
//   open <tenant>                 base URL becomes http://<tenant>.localhost:5173
//   login <email> <password>      sign in through the login form
//   nav <path>                    load a path and wait for its page heading
//   h1                            print the page heading
//   click <selector>              Playwright selector, e.g. text=Mark done, role=button[name="Save"]
//   fill <selector> <text>        type into an input
//   text <selector>               print an element's text
//   wait <selector>               wait for an element to appear
//   viewport <width> <height>     resize the page, e.g. viewport 390 844
//   ss [name]                     screenshot the viewport to $SHOTS_DIR (default /tmp/goerp-shots)
//   ssfull [name]                 full-page screenshot
//   errors                        print page errors and failed requests seen so far
//   eval <js>                     evaluate a JS expression in the page and print the result
//   quit                          close the browser
import { mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { createInterface } from "node:readline";
import { createRequire } from "node:module";

const pwDir = process.env.GOERP_PLAYWRIGHT_DIR ?? `${homedir()}/.cache/goerp-run-driver/`;
const { chromium } = createRequire(pwDir.endsWith("/") ? pwDir : `${pwDir}/`)("playwright-core");

const shotsDir = process.env.SHOTS_DIR ?? "/tmp/goerp-shots";
mkdirSync(shotsDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
let base = "http://localhost:5173";
let shotCount = 0;
const problems = [];

page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
page.on("console", (m) => m.type() === "error" && !m.text().startsWith("Failed to load resource") && problems.push(`console: ${m.text()}`));
page.on("response", (r) => {
  const url = r.url().replace(base, "");
  // /_notif/* has no backend yet (goerp#1112); /auth/me is 401 before sign-in.
  if (r.status() < 400 || url.startsWith("/_notif/") || (url === "/auth/me" && r.status() === 401)) return;
  problems.push(`${r.status()} ${r.request().method()} ${url}`);
});

async function heading() {
  const h1 = page.locator("h1").first();
  await h1.waitFor({ timeout: 15000 });
  // A cold load of a module view shows "Page not found" until /_meta/schema
  // arrives (goerp#1230); give it a few seconds to settle.
  for (let i = 0; i < 20 && (await h1.innerText()) === "Page not found"; i++) await page.waitForTimeout(250);
  return h1.innerText();
}

async function screenshot(name, fullPage) {
  const file = `${shotsDir}/${String(++shotCount).padStart(2, "0")}-${name || "shot"}.png`;
  await page.screenshot({ path: file, fullPage });
  return file;
}

const commands = {
  open: async ([tenant]) => {
    base = `http://${tenant}.localhost:5173`;
    return base;
  },
  login: async ([email, password]) => {
    await page.goto(`${base}/auth/login`);
    const company = page.getByLabel(/company/i);
    if (await company.count()) await company.fill(base.split("//")[1].split(".")[0]);
    await page.getByLabel(/email/i).fill(email);
    await page.getByLabel(/^password/i).fill(password);
    await page.getByRole("button", { name: /sign in|log in/i }).click();
    // The URL can already read "/" while the sign-in form is still up, so wait
    // for the form's heading to go instead.
    try {
      await page.getByRole("heading", { name: "Sign in" }).waitFor({ state: "detached", timeout: 15000 });
    } catch {
      const alert = await page.getByRole("alert").allInnerTexts();
      throw new Error(`still on the sign-in page${alert.length ? `: ${alert.join(" ")}` : ""}`);
    }
    await page.waitForLoadState("networkidle");
    return `signed in, at ${page.url()}`;
  },
  nav: async ([path]) => {
    await page.goto(`${base}${path}`);
    return `${page.url()} — ${await heading()}`;
  },
  h1: async () => heading(),
  click: async (args) => {
    await page.locator(args.join(" ")).first().click();
    return "clicked";
  },
  fill: async ([selector, ...text]) => {
    await page.locator(selector).first().fill(text.join(" "));
    return "filled";
  },
  text: async (args) => page.locator(args.join(" ")).first().innerText(),
  wait: async (args) => {
    await page.locator(args.join(" ")).first().waitFor({ timeout: 15000 });
    return "present";
  },
  viewport: async ([w, h]) => {
    await page.setViewportSize({ width: Number(w), height: Number(h) });
    return `${w}x${h}`;
  },
  ss: async ([name]) => screenshot(name, false),
  ssfull: async ([name]) => screenshot(name, true),
  errors: async () => (problems.length ? problems.join("\n") : "none"),
  eval: async (args) => JSON.stringify(await page.evaluate(args.join(" "))),
};

for await (const line of createInterface({ input: process.stdin })) {
  const [cmd, ...args] = line.trim().split(/\s+/);
  if (!cmd || cmd.startsWith("#")) continue;
  if (cmd === "quit") break;
  const run = commands[cmd];
  if (!run) {
    console.log(`? unknown command: ${cmd}`);
    continue;
  }
  try {
    console.log(`> ${line.trim()}\n${await run(args)}`);
  } catch (e) {
    console.log(`! ${cmd} failed: ${e.message.split("\n")[0]}`);
  }
}
await browser.close();
