import { afterEach, describe, expect, it, vi } from "vitest";
import type { Command } from "../chrome/command-types.js";
import { resolveModuleShortcuts, warnInDevelopment } from "./module-shortcuts.js";
import { SHELL_SHORTCUTS } from "./shell-shortcuts.js";

const RESERVED = SHELL_SHORTCUTS.flatMap((s) => s.shortcuts);

function command(id: string, shortcut?: string): Command {
  return shortcut === undefined ? { id, label: id, action: vi.fn() } : { id, label: id, shortcut, action: vi.fn() };
}

function resolve(commands: Command[]) {
  const warn = vi.fn();
  const accepted = resolveModuleShortcuts(commands, RESERVED, false, warn);
  return { ids: accepted.map((s) => s.command.id), warn };
}

describe("resolveModuleShortcuts", () => {
  it("accepts a shortcut that conflicts with nothing", () => {
    const { ids, warn } = resolve([command("new-contact", "N C"), command("no-shortcut")]);
    expect(ids).toEqual(["new-contact"]);
    expect(warn).not.toHaveBeenCalled();
  });

  it.each([
    ["an identical shell chord", "Mod+K"],
    ["the same chord spelled with Ctrl", "Ctrl+K"],
    ["an identical shell sequence", "G H"],
    ["the start of a shell sequence", "G"],
    ["a sequence that starts with a shell one", "G H X"],
    ["the Esc key", "Escape"],
    ["G A, even though only admins can use it", "G A"],
  ])("ignores %s, with a warning", (_, shortcut) => {
    const { ids, warn } = resolve([command("mod.cmd", shortcut)]);
    expect(ids).toEqual([]);
    expect(warn).toHaveBeenCalledWith(expect.stringContaining("conflicts with a shell shortcut"));
  });

  it("keeps a sequence that only shares its first step with a shell one", () => {
    expect(resolve([command("mod.cmd", "G X")]).ids).toEqual(["mod.cmd"]);
  });

  it("gives a shortcut two modules share to the one registered first", () => {
    const { ids, warn } = resolve([command("first", "N C"), command("second", "N C")]);
    expect(ids).toEqual(["first"]);
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('conflicts with command "first"'));
  });

  it("skips an unparseable shortcut, with a warning", () => {
    const { ids, warn } = resolve([command("mod.cmd", "Hyper+K")]);
    expect(ids).toEqual([]);
    expect(warn).toHaveBeenCalledWith(expect.stringContaining("invalid shortcut"));
  });
});

describe("warnInDevelopment", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
  });

  it("logs a console warning in development, once per message", () => {
    const consoleWarn = vi.spyOn(console, "warn").mockImplementation(() => {});
    warnInDevelopment("dev-only message");
    warnInDevelopment("dev-only message");
    expect(consoleWarn).toHaveBeenCalledTimes(1);
  });

  it("stays silent in production", () => {
    vi.stubEnv("DEV", false);
    const consoleWarn = vi.spyOn(console, "warn").mockImplementation(() => {});
    warnInDevelopment("production message");
    expect(consoleWarn).not.toHaveBeenCalled();
  });
});
