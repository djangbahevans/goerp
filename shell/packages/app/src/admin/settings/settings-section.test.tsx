import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SettingsSection } from "./settings-section.js";

function setBrowserOnline(online: boolean) {
  act(() => {
    window.dispatchEvent(new Event(online ? "online" : "offline"));
  });
}

afterEach(() => {
  setBrowserOnline(true);
  cleanup();
});

describe("SettingsSection", () => {
  it("disables Save while offline and re-enables it on reconnect", () => {
    const permissions = createPermissionContextValue({
      permissions: new Set(),
      fieldAccess: {},
      modulesEnabled: new Set(),
    });
    render(
      <PermissionContext.Provider value={permissions}>
        <SettingsSection title="General" dirty saving={false} onSave={vi.fn()}>
          <p>Fields</p>
        </SettingsSection>
      </PermissionContext.Provider>,
    );
    const save = () => screen.getByRole("button", { name: "Save" }) as HTMLButtonElement;
    expect(save().disabled).toBe(false);

    setBrowserOnline(false);
    expect(save().disabled).toBe(true);

    setBrowserOnline(true);
    expect(save().disabled).toBe(false);
  });
});
