import { toast } from "@goerp/sdk/notifications";
import { cleanup, fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { commandRegistry } from "../chrome/command-registry.js";
import type { Command } from "../chrome/command-types.js";
import { renderShell } from "./shortcuts-test-harness.js";

const unregisterFns: Array<() => void> = [];
function registerCommands(commands: Command[]): void {
  unregisterFns.push(commandRegistry.register(commands));
}

function press(key: string, init: KeyboardEventInit = {}, target: Element = document.body): void {
  fireEvent.keyDown(target, { key, ...init });
}

function pressSequence(keys: string, target?: Element): void {
  for (const key of keys.split(" ")) press(key.toLowerCase(), {}, target);
}

function paletteOpen(): boolean {
  return screen.queryByRole("dialog", { name: "Command palette" }) !== null;
}

function shortcutsOpen(): boolean {
  return screen.queryByRole("dialog", { name: "Keyboard shortcuts" }) !== null;
}

beforeEach(() => {
  // jsdom doesn't implement scrollIntoView at all.
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  for (const unregister of unregisterFns.splice(0)) unregister();
});

describe("GlobalShortcuts", () => {
  describe("shell shortcuts", () => {
    it("Ctrl+K opens the command palette", async () => {
      await renderShell();
      press("k", { ctrlKey: true });
      expect(paletteOpen()).toBe(true);
    });

    it("Ctrl+/ opens the keyboard shortcuts reference", async () => {
      await renderShell();
      press("/", { ctrlKey: true });
      expect(shortcutsOpen()).toBe(true);
    });

    it("? opens the keyboard shortcuts reference", async () => {
      await renderShell();
      press("?", { shiftKey: true });
      expect(shortcutsOpen()).toBe(true);
    });

    it.each([
      ["G H", "/"],
      ["G S", "/settings/profile"],
    ])("%s navigates to %s", async (keys, path) => {
      const router = await renderShell();
      pressSequence(keys);
      await vi.waitFor(() => expect(router.state.location.pathname).toBe(path));
    });

    it("G A takes an admin to /admin", async () => {
      const router = await renderShell({ roles: ["admin"] });
      pressSequence("G A");
      await vi.waitFor(() => expect(router.state.location.pathname).toBe("/admin"));
    });

    it("G A does nothing for a non-admin", async () => {
      const router = await renderShell({ roles: ["member"] });
      pressSequence("G A");
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(router.state.location.pathname).toBe("/elsewhere");
    });

    it("Esc closes the open reference", async () => {
      await renderShell();
      press("?");
      press("Escape", {}, screen.getByRole("dialog"));
      await vi.waitFor(() => expect(shortcutsOpen()).toBe(false));
    });
  });

  describe("text fields", () => {
    it.each(["Name", "Notes", "Editor"])("typing in %s triggers no plain-key shortcut", async (field) => {
      const router = await renderShell();
      const target = screen.getByRole("textbox", { name: field });
      press("?", { shiftKey: true }, target);
      pressSequence("G H", target);
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(shortcutsOpen()).toBe(false);
      expect(router.state.location.pathname).toBe("/elsewhere");
    });

    it("typing into a select trigger's type-ahead triggers no plain-key shortcut", async () => {
      const router = await renderShell();
      pressSequence("G H", screen.getByRole("combobox", { name: "Status" }));
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(router.state.location.pathname).toBe("/elsewhere");
    });

    it("an Alt chord typed in a text field stays text", async () => {
      const action = vi.fn();
      registerCommands([{ id: "accent", label: "Accent", shortcut: "Alt+E", action }]);
      await renderShell();
      const input = screen.getByRole("textbox", { name: "Name" });
      press("´", { altKey: true, code: "KeyE" }, input);
      press("e", { ctrlKey: true, altKey: true, code: "KeyE" }, input);
      expect(action).not.toHaveBeenCalled();
      press("´", { altKey: true, code: "KeyE" });
      expect(action).toHaveBeenCalledTimes(1);
    });

    it("a Ctrl chord still fires from a text field", async () => {
      await renderShell();
      press("k", { ctrlKey: true }, screen.getByRole("textbox", { name: "Name" }));
      expect(paletteOpen()).toBe(true);
    });

    it("a plain-key shortcut fires from a non-text control", async () => {
      await renderShell();
      press("?", {}, screen.getByRole("button", { name: "Plain button" }));
      expect(shortcutsOpen()).toBe(true);
    });
  });

  describe("ignored keystrokes", () => {
    it("fires no shortcut other than Esc while an overlay is open", async () => {
      const router = await renderShell();
      press("?");
      pressSequence("G H", screen.getByRole("dialog"));
      press("k", { ctrlKey: true }, screen.getByRole("dialog"));
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(router.state.location.pathname).toBe("/elsewhere");
      expect(paletteOpen()).toBe(false);
    });

    it("runs a held chord once, ignoring key repeat", async () => {
      const action = vi.fn();
      registerCommands([{ id: "sync", label: "Sync", shortcut: "Mod+Shift+Y", action }]);
      await renderShell();
      press("Y", { ctrlKey: true, shiftKey: true });
      press("Y", { ctrlKey: true, shiftKey: true, repeat: true });
      press("Y", { ctrlKey: true, shiftKey: true, repeat: true });
      expect(action).toHaveBeenCalledTimes(1);
    });

    it("ignores a keystroke a focused component already handled", async () => {
      await renderShell();
      const input = screen.getByRole("textbox", { name: "Name" });
      input.addEventListener("keydown", (event) => event.preventDefault());
      press("k", { ctrlKey: true }, input);
      expect(paletteOpen()).toBe(false);
    });
  });

  describe("module command shortcuts", () => {
    it("runs the command with the shell's command context", async () => {
      const action = vi.fn();
      registerCommands([{ id: "new-contact", label: "New Contact", shortcut: "N C", action }]);
      await renderShell();
      pressSequence("N C");
      expect(action).toHaveBeenCalledWith(
        expect.objectContaining({ navigate: expect.any(Function), user: expect.objectContaining({ id: "u1" }) }),
      );
    });

    it("toasts a rejected async command", async () => {
      const toastError = vi.spyOn(toast, "error").mockImplementation(() => {});
      registerCommands([
        {
          id: "sync",
          label: "Sync",
          shortcut: "Mod+Shift+Y",
          action: async () => Promise.reject(new Error("offline")),
        },
      ]);
      await renderShell();
      press("y", { ctrlKey: true, shiftKey: true });
      await vi.waitFor(() => expect(toastError).toHaveBeenCalledWith("offline"));
    });

    it("toasts a command that throws synchronously", async () => {
      const toastError = vi.spyOn(toast, "error").mockImplementation(() => {});
      registerCommands([
        {
          id: "broken",
          label: "Broken",
          shortcut: "B R",
          action: () => {
            throw new Error("boom");
          },
        },
      ]);
      await renderShell();
      pressSequence("B R");
      await vi.waitFor(() => expect(toastError).toHaveBeenCalledWith("boom"));
    });

    it("gives a shared shortcut to the command the user can run, not an earlier gated one", async () => {
      vi.spyOn(console, "warn").mockImplementation(() => {});
      const gated = vi.fn();
      const open = vi.fn();
      registerCommands([{ id: "gated", label: "Gated", shortcut: "N C", permission: "crm:admin", action: gated }]);
      registerCommands([{ id: "open", label: "Open", shortcut: "N C", action: open }]);
      await renderShell();
      pressSequence("N C");
      expect(gated).not.toHaveBeenCalled();
      expect(open).toHaveBeenCalledTimes(1);
    });

    it("doesn't run a command whose permission the user lacks", async () => {
      const action = vi.fn();
      registerCommands([{ id: "archive", label: "Archive", shortcut: "A R", permission: "crm:archive", action }]);
      await renderShell();
      pressSequence("A R");
      expect(action).not.toHaveBeenCalled();
    });

    it("runs a permission-gated command once the user has the permission", async () => {
      const action = vi.fn();
      registerCommands([{ id: "archive", label: "Archive", shortcut: "A R", permission: "crm:archive", action }]);
      await renderShell({ permissions: ["crm:archive"] });
      pressSequence("A R");
      expect(action).toHaveBeenCalled();
    });

    it("ignores a shortcut that conflicts with a shell one, warning in development", async () => {
      const consoleWarn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const action = vi.fn();
      registerCommands([{ id: "mod.search", label: "Search module", shortcut: "Mod+K", action }]);
      await renderShell();
      press("k", { ctrlKey: true });
      expect(action).not.toHaveBeenCalled();
      expect(paletteOpen()).toBe(true);
      expect(consoleWarn).toHaveBeenCalledWith(expect.stringContaining('"mod.search"'));
    });
  });
});
