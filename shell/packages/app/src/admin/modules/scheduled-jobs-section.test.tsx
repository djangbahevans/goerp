import { apiClient } from "@goerp/sdk";
import { createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { type MessageHandler, tenantChannel, wsManager } from "@goerp/sdk/realtime";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ModuleSummary } from "./admin-modules-api.js";
import { type CronJob, type CronJobs, cronKeys } from "./cron-jobs-api.js";
import { ScheduledJobsSection } from "./scheduled-jobs-section.js";

const auth = vi.hoisted(() => ({
  isAuthenticated: true,
  tenant: { id: "tenant-a" },
  user: { id: "admin", roles: ["admin"] },
}));
vi.mock("@goerp/sdk/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@goerp/sdk/auth")>()),
  useAuth: () => auth,
}));

const module: ModuleSummary = {
  name: "contacts",
  displayName: "Contacts",
  description: "",
  version: "1.0.0",
  type: "standard",
  entitled: true,
  enabled: true,
  dependsOn: [],
  permissions: [],
};
const job: CronJob = {
  name: "dedupe",
  label: "Duplicate scan",
  description: "Find duplicate contacts",
  schedule: "0 3 * * 0",
  queue: "bulk",
  timeout_seconds: 3600,
  enabled_by_default: false,
  enabled: false,
  generation: "019a4700-0000-7000-8000-000000000001",
  effective_enabled: false,
  blocked_reason: "job_disabled",
  next_run_at: null,
  updated_at: "2026-10-08T10:00:00Z",
};
const list = (jobs: CronJob[] = [job]): CronJobs => ({
  module: module.name,
  entitled: true,
  module_enabled: true,
  module_ready: true,
  cron_jobs: jobs,
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const node = () => (
    <QueryClientProvider client={client}>
      <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
        <ScheduledJobsSection module={module} />
      </PermissionContext.Provider>
    </QueryClientProvider>
  );
  const view = render(node());
  return { client, rerender: () => view.rerender(node()) };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  auth.tenant.id = "tenant-a";
  auth.user.roles = ["admin"];
});

describe("Scheduled jobs", () => {
  it.each(["modules.changed", "module.installed", "plan.changed", "schema.updated"])(
    "refreshes current declarations on %s without a version change",
    async (type) => {
      const listeners: MessageHandler[] = [];
      vi.spyOn(wsManager, "subscribe").mockImplementation((channel, listener) => {
        if (channel === tenantChannel(auth.tenant.id)) listeners.push(listener);
        return vi.fn();
      });
      let current = list();
      const get = vi.spyOn(apiClient, "get").mockImplementation(async () => current);
      mount();
      const input = await screen.findByRole("switch");
      input.focus();
      current = list([{ ...job, name: "replacement", label: "Replacement job" }]);

      act(() => {
        for (const listener of listeners) listener({ channel: tenantChannel(auth.tenant.id), type });
      });

      await screen.findByRole("switch", { name: "Enable Replacement job" });
      expect(get).toHaveBeenCalledTimes(2);
      expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Scheduled jobs" }));
    },
  );

  it("loads independently, preserves API order and distinguishes identical labels", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue(list([job, { ...job, name: "second" }]));
    mount();
    const switches = await screen.findAllByRole("switch");
    expect(switches.map((input) => input.getAttribute("id"))).toHaveLength(2);
    expect(screen.getByLabelText("Enable Duplicate scan (dedupe)")).toBe(switches[0]);
    expect(screen.getByLabelText("Enable Duplicate scan (second)")).toBe(switches[1]);
    expect(screen.getAllByText("0 3 * * 0")).toHaveLength(2);
  });

  it("keeps the confirmed state and focus while saving, blocks a second submission, and announces success", async () => {
    const pending = deferred<CronJob>();
    let current = list();
    vi.spyOn(apiClient, "get").mockImplementation(async () => current);
    const patch = vi.spyOn(apiClient, "patch").mockImplementation(() => pending.promise);
    mount();
    const input = (await screen.findByRole("switch")) as HTMLInputElement;
    input.focus();
    fireEvent.click(input);
    expect(input.checked).toBe(false);
    expect(input.disabled).toBe(false);
    expect(document.activeElement).toBe(input);
    expect(screen.getByText("Saving…")).toBeTruthy();
    fireEvent.click(input);
    expect(patch).toHaveBeenCalledTimes(1);
    expect(patch).toHaveBeenCalledWith(
      "/admin/modules/contacts/cron-jobs/dedupe",
      {
        enabled: true,
        expected_generation: job.generation,
      },
      { retryNetworkErrors: false },
    );
    const saved = {
      ...job,
      enabled: true,
      generation: "new-generation",
      blocked_reason: null,
      effective_enabled: true,
    };
    current = list([saved]);
    await act(async () => pending.resolve(saved));
    await waitFor(() => expect(input.checked).toBe(true));
    expect(document.activeElement).toBe(input);
    expect(screen.getByRole("status").textContent).toBe("Duplicate scan enabled");
  });

  it("keeps other rows usable while a row is saving", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue(list([job, { ...job, name: "other", label: "Other job" }]));
    const pending = deferred<CronJob>();
    const patch = vi.spyOn(apiClient, "patch").mockImplementation(() => pending.promise);
    mount();
    const inputs = await screen.findAllByRole("switch");
    fireEvent.click(inputs[0]!);
    fireEvent.click(inputs[1]!);
    expect(patch).toHaveBeenCalledTimes(2);
  });

  it("reconciles a generation conflict before permitting another edit", async () => {
    const current = {
      ...job,
      enabled: true,
      generation: "server-generation",
      blocked_reason: null,
      effective_enabled: true,
    };
    const get = vi
      .spyOn(apiClient, "get")
      .mockResolvedValueOnce(list())
      .mockResolvedValue(list([current]));
    vi.spyOn(apiClient, "patch").mockRejectedValue(
      new AppError({ code: "cron_settings_conflict", message: "conflict", httpStatus: 409 }),
    );
    mount();
    const input = (await screen.findByRole("switch")) as HTMLInputElement;
    fireEvent.click(input);
    await screen.findByText("This job changed in another session. Check its current state and try again.");
    await waitFor(() => expect(input.checked).toBe(true));
    expect(get).toHaveBeenCalledTimes(2);
    expect(input.disabled).toBe(false);
    expect(input.getAttribute("aria-describedby")).toContain("error");
  });

  it("reads server state after an uncertain save without submitting an inverse toggle", async () => {
    const current = {
      ...job,
      enabled: true,
      generation: "committed-generation",
      blocked_reason: null,
      effective_enabled: true,
    };
    vi.spyOn(apiClient, "get")
      .mockResolvedValueOnce(list())
      .mockResolvedValue(list([current]));
    const patch = vi.spyOn(apiClient, "patch").mockRejectedValue(new TypeError("connection lost"));
    mount();
    const input = (await screen.findByRole("switch")) as HTMLInputElement;
    fireEvent.click(input);
    await waitFor(() => expect(input.checked).toBe(true));
    expect(patch).toHaveBeenCalledTimes(1);
  });

  it("keeps an uncertain row unavailable until Retry successfully refreshes state", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValueOnce(list()).mockRejectedValue(new TypeError("offline"));
    vi.spyOn(apiClient, "patch").mockRejectedValue(new TypeError("offline"));
    mount();
    const input = (await screen.findByRole("switch")) as HTMLInputElement;
    fireEvent.click(input);
    await waitFor(() => expect(input.disabled).toBe(true));
    await waitFor(() => expect(screen.queryByText("Saving…")).toBeNull());
    get.mockResolvedValue(list());
    fireEvent.click(screen.getAllByRole("button", { name: "Retry" }).at(-1)!);
    await waitFor(() => expect(input.disabled).toBe(false));
  });

  it.each(["module_not_entitled", "module_not_ready"] as const)(
    "disables switches for %s while showing saved choices",
    async (reason) => {
      vi.spyOn(apiClient, "get").mockResolvedValue(list([{ ...job, enabled: true, blocked_reason: reason }]));
      mount();
      const input = (await screen.findByRole("switch")) as HTMLInputElement;
      expect(input.checked).toBe(true);
      expect(input.disabled).toBe(true);
      expect(
        screen.getByText(
          reason === "module_not_entitled" ? "Your plan does not include this module." : "This module is unavailable.",
        ),
      ).toBeTruthy();
    },
  );

  it("keeps module-disabled saved choices editable and formats estimates in UTC", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue(
      list([{ ...job, enabled: true, blocked_reason: "module_disabled", next_run_at: "2026-10-11T03:00:00Z" }]),
    );
    mount();
    const input = (await screen.findByRole("switch")) as HTMLInputElement;
    expect(input.checked).toBe(true);
    expect(input.disabled).toBe(false);
    expect(screen.getByText("Enable this module to run its scheduled jobs.")).toBeTruthy();
    expect(screen.getByText("Next scheduled: 2026-10-11 03:00:00 UTC")).toBeTruthy();
  });

  it("shows inline load errors with Retry and an empty state", async () => {
    const get = vi.spyOn(apiClient, "get").mockRejectedValue(new Error("unavailable"));
    mount();
    await screen.findByText("Couldn't load scheduled jobs");
    get.mockResolvedValue(list([]));
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await screen.findByText("This module has no scheduled jobs.");
  });

  it("moves focus to the heading when a focused job disappears", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue(list());
    const { client } = mount();
    const input = await screen.findByRole("switch");
    input.focus();
    act(() => client.setQueryData(cronKeys.module("tenant-a", "admin", "contacts"), list([])));
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Scheduled jobs" })));
    expect(screen.getByRole("status").textContent).toBe("The scheduled job is unavailable.");
  });

  it("discards previous tenant controls and pending responses on tenant switch", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue(list());
    const pending = deferred<CronJob>();
    vi.spyOn(apiClient, "patch").mockImplementation(() => pending.promise);
    const view = mount();
    fireEvent.click(await screen.findByRole("switch"));
    auth.tenant.id = "tenant-b";
    get.mockResolvedValue(list([{ ...job, label: "Tenant B job" }]));
    view.rerender();
    await screen.findByRole("switch", { name: "Enable Tenant B job" });
    await act(async () => pending.resolve({ ...job, enabled: true }));
    expect(screen.queryByText("Duplicate scan enabled")).toBeNull();
    expect((screen.getByRole("switch") as HTMLInputElement).checked).toBe(false);
    expect(view.client.getQueryData(cronKeys.module("tenant-a", "admin", "contacts"))).toBeUndefined();
  });
});
