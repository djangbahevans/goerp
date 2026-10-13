import { useQuery } from "@tanstack/react-query";
import { act, cleanup, render, renderHook, waitFor } from "@testing-library/react";
import { StrictMode, useContext, useLayoutEffect } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { usePermission } from "../auth/use-permission.js";
import { AppError } from "../error/app-error.js";
import { apiClient } from "../http/api-client.js";
import { tenantFormatStore } from "../i18n/tenant-format.js";
import { useLocale } from "../i18n/use-locale.js";
import { defineModule } from "../module/define-module.js";
import { moduleApiRegistry } from "../module/module-api-registry.js";
import { permissionRef } from "../names.js";
import { useModule } from "../react/use-module.js";
import { useTheme } from "../react/use-theme.js";
import { useRealtimeChannel } from "../realtime/use-realtime-channel.js";
import { wsManager } from "../realtime/ws-manager.js";
import { ViewRegistryContext } from "../schema/view-registry-provider.js";
import { fireEvent, ShellProvider, screen, simulateRealtimeMessage } from "./index.js";

const contactsApi = {
  listContacts: () => apiClient.get<{ data: { id: string; name: string }[] }>("/_m/contacts/contacts"),
  getContact: (id: string) => apiClient.get<{ id: string; name: string }>(`/_m/contacts/contacts/${id}`),
};

afterEach(() => {
  cleanup();
  moduleApiRegistry.register("contacts", undefined);
  vi.restoreAllMocks();
});

describe("ShellProvider", () => {
  it("gates elements by explicit permissions, including foreign refs", () => {
    function Probe() {
      const canRead = usePermission("contacts:contact:read");
      const foreign = usePermission(permissionRef("hr:employee:read"));
      return (
        <>
          {canRead && <button type="button">Read contact</button>}
          {foreign && <span>Employee</span>}
        </>
      );
    }
    const { rerender } = render(
      <ShellProvider module="contacts">
        <Probe />
      </ShellProvider>,
    );
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.queryByText("Employee")).toBeNull();
    rerender(
      <ShellProvider module="contacts" permissions={["contacts:contact:read", permissionRef("hr:employee:read")]}>
        <Probe />
      </ShellProvider>,
    );
    expect(screen.getByRole("button", { name: "Read contact" })).toBeDefined();
    expect(screen.getByText("Employee")).toBeDefined();
  });

  it("provides a partial user, tenant, ready view registry and appearance before child layout effects", async () => {
    const fetch = vi.spyOn(globalThis, "fetch");
    const { result } = renderHook(
      () => {
        const ctx = useModule("contacts");
        const locale = useLocale();
        const theme = useTheme();
        const registry = useContext(ViewRegistryContext);
        useLayoutEffect(() => {
          void apiClient.get("/contacts");
        }, []);
        return { ctx, locale, theme, registry };
      },
      {
        wrapper: ({ children }) => (
          <ShellProvider
            module="contacts"
            user={{ id: "u1", roles: ["admin"], locale: "ar", theme: "dark" }}
            apiMocks={{ "GET /contacts": [] }}
          >
            {children}
          </ShellProvider>
        ),
      },
    );
    expect(result.current.ctx.user.id).toBe("u1");
    expect(result.current.ctx.user.roles).toEqual(["admin"]);
    expect(result.current.ctx.tenant.slug).toBe("testing");
    expect(result.current.locale.locale).toBe("ar");
    expect(result.current.locale.direction).toBe("rtl");
    expect(result.current.theme.theme).toBe("dark");
    expect(result.current.registry?.navigationTree).toEqual([]);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("returns JSON bodies through a registered API and surfaces 404 and unmocked AppErrors", async () => {
    defineModule({ name: "contacts", api: contactsApi });
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const { result } = renderHook(() => useModule("contacts"), {
      wrapper: ({ children }) => (
        <ShellProvider
          module="contacts"
          apiMocks={{
            "GET /contacts/contacts": { data: [{ id: "c1", name: "Ada" }] },
            "GET /contacts/contacts/c1": null,
          }}
        >
          {children}
        </ShellProvider>
      ),
    });
    const api = result.current.api as typeof contactsApi;
    expect(api).toBe(contactsApi);
    await expect(api.listContacts()).resolves.toEqual({ data: [{ id: "c1", name: "Ada" }] });
    await expect(api.getContact("c1")).rejects.toBeInstanceOf(AppError);
    await expect(api.getContact("c1")).rejects.toMatchObject({ httpStatus: 404 });
    await expect(api.getContact("c2")).rejects.toMatchObject({ code: "testing.unmocked_request", httpStatus: 501 });
    expect(error).toHaveBeenCalledWith("Unmocked API request: GET /_m/contacts/contacts/c2");
  });

  it("cleans up global fetch, appearance, persisted preferences, and query cache in StrictMode", () => {
    const originalFetch = globalThis.fetch;
    const stored = ["goerp-theme", "goerp-locale", "goerp-contrast"].map((key) => localStorage.getItem(key));
    const html = document.documentElement.outerHTML.split("><head")[0];
    const format = tenantFormatStore.get();
    const { result, unmount } = renderHook(() => useModule("contacts"), {
      wrapper: ({ children }) => (
        <StrictMode>
          <ShellProvider module="contacts" user={{ theme: "dark", locale: "ar" }}>
            {children}
          </ShellProvider>
        </StrictMode>
      ),
    });
    const client = result.current.queryClient;
    client.setQueryData(["contact"], "cached");
    expect(globalThis.fetch).not.toBe(originalFetch);
    unmount();
    expect(globalThis.fetch).toBe(originalFetch);
    expect(client.getQueryData(["contact"])).toBeUndefined();
    expect(["goerp-theme", "goerp-locale", "goerp-contrast"].map((key) => localStorage.getItem(key))).toEqual(stored);
    expect(document.documentElement.outerHTML.split("><head")[0]).toBe(html);
    expect(tenantFormatStore.get()).toEqual(format);
  });

  it("keeps the active mock when an older shell unmounts first", async () => {
    const originalFetch = globalThis.fetch;
    const first = render(
      <ShellProvider module="contacts" apiMocks={{ "GET /contacts": ["first"] }}>
        {null}
      </ShellProvider>,
    );
    const second = render(
      <ShellProvider module="contacts" apiMocks={{ "GET /contacts": ["second"] }}>
        {null}
      </ShellProvider>,
    );
    first.unmount();
    await expect(apiClient.get("/contacts")).resolves.toEqual(["second"]);
    second.unmount();
    expect(globalThis.fetch).toBe(originalFetch);
  });

  it("re-exports Testing Library events and query helpers", () => {
    const clicked = vi.fn();
    render(
      <ShellProvider module="contacts">
        <button type="button" onClick={clicked}>
          Test
        </button>
      </ShellProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Test" }));
    expect(clicked).toHaveBeenCalledOnce();
  });
});

describe("simulateRealtimeMessage", () => {
  it("delivers through channel handlers and refetches active list queries", async () => {
    defineModule({ name: "contacts", api: contactsApi });
    const listContacts = vi.spyOn(contactsApi, "listContacts");
    const onMessage = vi.fn();
    const otherChannel = vi.fn();
    const unsubscribe = wsManager.subscribe("feed:other", otherChannel);
    function List() {
      const ctx = useModule("contacts");
      const api = ctx.api as typeof contactsApi;
      const query = useQuery({ queryKey: ["contacts"], queryFn: () => api.listContacts() });
      useRealtimeChannel("feed:contacts", (message) => {
        onMessage(message);
        void ctx.queryClient.invalidateQueries({ queryKey: ["contacts"] });
      });
      return <span>{query.data?.data[0]?.name ?? "Loading"}</span>;
    }
    const { unmount } = render(
      <ShellProvider module="contacts" apiMocks={{ "GET /contacts/contacts": { data: [{ id: "c1", name: "Ada" }] } }}>
        <List />
      </ShellProvider>,
    );
    await screen.findByText("Ada");
    expect(listContacts).toHaveBeenCalledOnce();
    act(() =>
      simulateRealtimeMessage("feed:contacts", {
        type: "record.updated",
        resource: "contacts.contact",
        recordId: "c1",
        record: { id: "c1", name: "Ada" },
      }),
    );
    expect(onMessage).toHaveBeenCalledWith({
      type: "record.updated",
      event: "record.updated",
      resource: "contacts.contact",
      recordId: "c1",
      record: { id: "c1", name: "Ada" },
    });
    await waitFor(() => expect(listContacts).toHaveBeenCalledTimes(2));
    expect(otherChannel).not.toHaveBeenCalled();
    unmount();
    simulateRealtimeMessage("feed:contacts", { type: "record.deleted", resource: "contacts.contact", recordId: "c1" });
    expect(onMessage).toHaveBeenCalledOnce();
    unsubscribe();
  });

  it("preserves custom event names and payloads through the realtime adapter", () => {
    const handler = vi.fn();
    const { unmount } = renderHook(() => useRealtimeChannel("feed:contacts", handler), {
      wrapper: ({ children }) => <ShellProvider module="contacts">{children}</ShellProvider>,
    });
    act(() =>
      simulateRealtimeMessage("feed:contacts", { type: "custom", event: "contacts.sync_done", payload: { count: 5 } }),
    );
    expect(handler).toHaveBeenCalledWith({ type: "custom", event: "contacts.sync_done", payload: { count: 5 } });
    unmount();
  });
});
