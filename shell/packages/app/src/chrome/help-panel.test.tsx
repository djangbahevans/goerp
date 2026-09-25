import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { openKeyboardShortcuts } from "../shortcuts/keyboard-shortcuts-control.js";
import { renderShell } from "../shortcuts/shortcuts-test-harness.js";
import { HelpButton } from "./help-button.js";
import { type HelpLink, HelpPanel } from "./help-panel.js";

const ALL_LINKS: HelpLink[] = [
  { label: "Documentation", icon: "book-open", href: "https://docs.example.com" },
  { label: "API reference", icon: "code", href: "https://api.example.com" },
  { label: "Status page", icon: "activity", href: "https://status.example.com" },
  { label: "What's new", icon: "sparkles", href: "https://example.com/changelog" },
];

async function renderPanel(links: HelpLink[], onClose = vi.fn()) {
  await renderShell({ extra: <HelpPanel open onClose={onClose} links={links} /> });
  return screen.getByRole("dialog", { name: "Help" });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("HelpPanel", () => {
  it("lists every configured link in order, each opening in a new tab", async () => {
    const panel = await renderPanel(ALL_LINKS);
    const links = within(within(panel).getByRole("region", { name: "Links" })).getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual([
      "Documentation (opens in a new tab)",
      "API reference (opens in a new tab)",
      "Status page (opens in a new tab)",
      "What's new (opens in a new tab)",
    ]);
    expect(links.map((link) => link.getAttribute("href"))).toEqual(ALL_LINKS.map((link) => link.href));
    for (const link of links) {
      expect(link.getAttribute("target")).toBe("_blank");
      expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    }
  });

  it("hides a link whose URL is unset, empty or blank", async () => {
    const panel = await renderPanel([
      { label: "Documentation", icon: "book-open", href: "https://docs.example.com" },
      { label: "API reference", icon: "code", href: undefined },
      { label: "Status page", icon: "activity", href: "" },
      { label: "What's new", icon: "sparkles", href: "  " },
    ]);
    expect(
      within(panel)
        .getAllByRole("link")
        .map((link) => link.getAttribute("href")),
    ).toEqual(["https://docs.example.com"]);
  });

  it("omits the Links section entirely when no link is configured", async () => {
    const panel = await renderPanel(ALL_LINKS.map((link) => ({ ...link, href: undefined })));
    expect(within(panel).queryByRole("region", { name: "Links" })).toBeNull();
    expect(within(panel).queryByRole("link")).toBeNull();
    expect(within(panel).getByRole("region", { name: "Keyboard shortcuts" })).toBeTruthy();
  });

  it("shows the shortcuts reference with its groups nested under the section heading", async () => {
    const panel = await renderPanel([]);
    const section = within(panel).getByRole("region", { name: "Keyboard shortcuts" });
    expect(within(section).getByRole("heading", { level: 3, name: "Keyboard shortcuts" })).toBeTruthy();
    expect(within(section).getByRole("heading", { level: 4, name: "General" })).toBeTruthy();
    expect(within(section).getByText("Open command palette")).toBeTruthy();
  });

  it("moves focus to its heading on open", async () => {
    await renderPanel(ALL_LINKS);
    await vi.waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("heading", { level: 2, name: "Help" })),
    );
  });

  it("calls onClose from Escape and from its close button", async () => {
    const onClose = vi.fn();
    const panel = await renderPanel(ALL_LINKS, onClose);
    fireEvent.keyDown(panel, { key: "Escape" });
    fireEvent.click(within(panel).getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});

describe("HelpButton", () => {
  it("opens the panel, and Escape closes it with focus back on the button", async () => {
    await renderShell({ extra: <HelpButton /> });
    const button = screen.getByRole("button", { name: "Help" });
    button.focus();
    fireEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("true");
    fireEvent.keyDown(screen.getByRole("dialog", { name: "Help" }), { key: "Escape" });
    expect(button.getAttribute("aria-expanded")).toBe("false");
    await vi.waitFor(() => expect(screen.queryByRole("dialog", { name: "Help" })).toBeNull());
    expect(document.activeElement).toBe(button);
  });

  it("Escape closes only the shortcuts dialog when it's open over the panel", async () => {
    await renderShell({ extra: <HelpButton /> });
    fireEvent.click(screen.getByRole("button", { name: "Help" }));
    act(() => openKeyboardShortcuts());
    fireEvent.keyDown(screen.getByRole("dialog", { name: "Keyboard shortcuts" }), { key: "Escape" });
    await vi.waitFor(() => expect(screen.queryByRole("dialog", { name: "Keyboard shortcuts" })).toBeNull());
    expect(screen.getByRole("dialog", { name: "Help" })).toBeTruthy();
  });
});
