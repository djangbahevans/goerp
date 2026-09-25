import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { commandRegistry } from "../chrome/command-registry.js";
import type { Command } from "../chrome/command-types.js";
import { openKeyboardShortcuts } from "./keyboard-shortcuts-control.js";
import { renderShell } from "./shortcuts-test-harness.js";

const unregisterFns: Array<() => void> = [];
function registerCommands(commands: Command[]): void {
  unregisterFns.push(commandRegistry.register(commands));
}

async function openDialog(opts: Parameters<typeof renderShell>[0] = {}) {
  await renderShell(opts);
  act(() => openKeyboardShortcuts());
  return screen.getByRole("dialog", { name: "Keyboard shortcuts" });
}

function group(dialog: HTMLElement, name: string): HTMLElement | null {
  const heading = within(dialog).queryByRole("heading", { name });
  return heading?.closest("section") ?? null;
}

function labels(section: HTMLElement | null): string[] {
  return section ? [...section.querySelectorAll("dt")].map((dt) => dt.textContent ?? "") : [];
}

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  for (const unregister of unregisterFns.splice(0)) unregister();
});

describe("KeyboardShortcutsDialog", () => {
  it("isn't rendered until opened", async () => {
    await renderShell();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("lists the shell's General and Navigation shortcuts in order", async () => {
    const dialog = await openDialog({ roles: ["admin"] });
    expect(labels(group(dialog, "General"))).toEqual([
      "Open command palette",
      "Open keyboard shortcuts",
      "Close the topmost overlay",
    ]);
    expect(labels(group(dialog, "Navigation"))).toEqual(["Go home", "Go to settings", "Go to admin"]);
  });

  it("omits Go to admin for a non-admin", async () => {
    const dialog = await openDialog({ roles: ["member"] });
    expect(labels(group(dialog, "Navigation"))).toEqual(["Go home", "Go to settings"]);
  });

  it("shows each step as its own key, joined by then and or", async () => {
    const dialog = await openDialog();
    const row = within(dialog).getByText("Open keyboard shortcuts").closest("div") as HTMLElement;
    expect([...row.querySelectorAll("kbd")].map((kbd) => kbd.textContent)).toEqual(["Ctrl /", "?"]);
    expect(within(row).getByText("or")).toBeTruthy();
    const home = within(dialog).getByText("Go home").closest("div") as HTMLElement;
    expect([...home.querySelectorAll("kbd")].map((kbd) => kbd.textContent)).toEqual(["G", "H"]);
    expect(within(home).getByText("then")).toBeTruthy();
  });

  it("spells each shortcut out for a screen reader and hides the glyphs", async () => {
    const dialog = await openDialog();
    const row = within(dialog).getByText("Open keyboard shortcuts").closest("div") as HTMLElement;
    expect(within(row).getByText("Control slash or question mark").className).toContain("sr-only");
    expect(row.querySelector("kbd")?.closest('[aria-hidden="true"]')).not.toBeNull();
  });

  it("lists module shortcuts under Commands, sorted by label", async () => {
    registerCommands([
      { id: "new-order", label: "New Order", shortcut: "N O", action: vi.fn() },
      { id: "new-contact", label: "New Contact", shortcut: "N C", action: vi.fn() },
      { id: "no-shortcut", label: "Unbound", action: vi.fn() },
    ]);
    const dialog = await openDialog();
    expect(labels(group(dialog, "Commands"))).toEqual(["New Contact", "New Order"]);
  });

  it("leaves out conflicting and permission-gated module shortcuts", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    registerCommands([
      { id: "clash", label: "Clash", shortcut: "G H", action: vi.fn() },
      { id: "gated", label: "Gated", shortcut: "A R", permission: "crm:archive", action: vi.fn() },
    ]);
    const dialog = await openDialog();
    expect(group(dialog, "Commands")).toBeNull();
  });

  it("closes from its close button and returns focus to where it opened from", async () => {
    await renderShell();
    const button = screen.getByRole("button", { name: "Plain button" });
    button.focus();
    act(() => openKeyboardShortcuts());
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }));
    await vi.waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // Radix restores focus a tick after the dialog unmounts.
    await vi.waitFor(() => expect(document.activeElement).toBe(button));
  });

  it("returns focus to the menu trigger when the menu item that opened it has unmounted", async () => {
    await renderShell();
    const trigger = screen.getByRole("button", { name: "Plain button" });
    const item = document.createElement("button");
    document.body.append(item);
    item.focus();
    // A menu item opens the dialog, then the menu closes and refocuses its trigger.
    act(() => {
      openKeyboardShortcuts();
      item.remove();
      trigger.focus();
    });
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }));
    await vi.waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await vi.waitFor(() => expect(document.activeElement).toBe(trigger));
  });
});
