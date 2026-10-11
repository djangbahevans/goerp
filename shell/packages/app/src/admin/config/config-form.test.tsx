import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type ConfigEntry, toEntry, useSaveModuleConfig } from "./config-api.js";
import { ConfigForm } from "./config-form.js";

function entry(overrides: Partial<ConfigEntry> = {}): ConfigEntry {
  return {
    ...toEntry({
      key: "prefix",
      label: "Prefix",
      type: "string",
      required: false,
      encrypted: false,
      generated: false,
      restart_required: false,
      default: null,
      is_set: true,
      value: "INV",
    }),
    ...overrides,
  };
}

function Harness({ entries }: { entries: ConfigEntry[] }) {
  const save = useSaveModuleConfig("sales", []);
  return <ConfigForm moduleName="sales" entries={entries} save={save} onSaved={() => {}} />;
}

function mount(entries: ConfigEntry[]) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <Harness entries={entries} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("settings saves", () => {
  it("submits changed keys while preserving masked and generated settings", async () => {
    const patch = vi.spyOn(apiClient, "patch").mockResolvedValue({ updated: ["sales.prefix"] });
    mount([
      entry(),
      entry({ key: "secret", label: "Secret", encrypted: true, fieldType: "country_select", value: "***" }),
      entry({ key: "token", label: "Token", generated: true, value: "generated" }),
    ]);
    fireEvent.change(screen.getByLabelText("Prefix"), { target: { value: "NEW" } });
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/admin/config", { "sales.prefix": "NEW" }));
  });

  it("resets a required setting with a default without native validation blocking the reset", async () => {
    const patch = vi.spyOn(apiClient, "patch").mockResolvedValue({ updated: ["sales.prefix"] });
    const view = mount([entry({ required: true, default: "INV" })]);
    expect(view.container.querySelector("form")?.noValidate).toBe(true);
    fireEvent.change(screen.getByLabelText(/Prefix/), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/admin/config", { "sales.prefix": null }));
  });

  it("saves false for a required boolean checkbox", async () => {
    const patch = vi.spyOn(apiClient, "patch").mockResolvedValue({ updated: ["sales.enabled"] });
    mount([
      entry({ key: "enabled", label: "Enabled", type: "boolean", fieldType: "boolean", required: true, value: true }),
    ]);
    const input = screen.getByRole("checkbox", { name: "Enabled" }) as HTMLInputElement;
    expect(input.required).toBe(false);
    fireEvent.click(input);
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/admin/config", { "sales.enabled": false }));
  });

  it("attaches server validation errors to the affected field", async () => {
    vi.spyOn(apiClient, "patch").mockRejectedValue(
      new AppError({
        code: "invalid_config",
        message: "Invalid configuration",
        httpStatus: 422,
        details: { "sales.prefix": "Prefix is reserved" },
      }),
    );
    mount([entry()]);
    fireEvent.change(screen.getByLabelText("Prefix"), { target: { value: "RESERVED" } });
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe("Prefix is reserved"));
    expect(screen.getByLabelText("Prefix").getAttribute("aria-invalid")).toBe("true");
  });

  it("keeps unsupported declarations out of editable saves", async () => {
    const patch = vi.spyOn(apiClient, "patch").mockResolvedValue({ updated: ["sales.prefix"] });
    mount([entry(), entry({ key: "relation", label: "Relation", fieldType: "relation" })]);
    fireEvent.change(screen.getByLabelText("Prefix"), { target: { value: "NEW" } });
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/admin/config", { "sales.prefix": "NEW" }));
  });
});
