import { afterEach, describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { TranslationLoader } from "./translation-loader.js";
import { TranslationStore } from "./translation-store.js";
import { LocaleStore } from "./use-locale.js";

const notFound = () => new AppError({ code: "not_found", message: "not found", httpStatus: 404 });

// Serves files keyed by "{module}/{locale}"; anything else is a 404.
function fakeClient(files: Record<string, Record<string, string>>) {
  return {
    get: vi.fn(async (path: string) => {
      const match = /^\/modules\/([^/]+)\/translations\/([^/]+)\.json$/.exec(path);
      const file = match
        ? files[`${decodeURIComponent(match[1] ?? "")}/${decodeURIComponent(match[2] ?? "")}`]
        : undefined;
      if (!file) throw notFound();
      return file;
    }),
  };
}

function setup(locale: string, files: Record<string, Record<string, string>>) {
  const store = new TranslationStore();
  const locales = new LocaleStore();
  locales.setLocale(locale);
  const client = fakeClient(files);
  const loader = new TranslationLoader(store, locales, client as never);
  return { store, locales, client, loader };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("TranslationLoader", () => {
  it("fetches every file in the current locale's fallback chain", async () => {
    const { store, client, loader } = setup("fr-GH", {
      "contacts/fr": { "actions.edit": "Modifier" },
      "contacts/en": { "actions.edit": "Edit", "actions.delete": "Archive" },
    });

    await loader.load("contacts");

    expect(client.get.mock.calls.map(([path]) => path)).toEqual([
      "/modules/contacts/translations/fr-GH.json",
      "/modules/contacts/translations/fr.json",
      "/modules/contacts/translations/en.json",
    ]);
    expect(store.translate("contacts", "fr-GH", "actions.edit")).toBe("Modifier");
    expect(store.translate("contacts", "fr-GH", "actions.delete")).toBe("Archive");
  });

  it("doesn't refetch a loaded file or a 404, and shares an in-flight request", async () => {
    const { client, loader } = setup("fr", { "contacts/en": {} });

    await Promise.all([loader.load("contacts"), loader.load("contacts")]);
    await loader.load("contacts");

    expect(client.get).toHaveBeenCalledTimes(2);
  });

  it("fetches only the missing files when the locale changes", async () => {
    const { store, locales, client, loader } = setup("en", {
      "contacts/en": { "actions.edit": "Edit" },
      "contacts/fr": { "actions.edit": "Modifier" },
    });
    await loader.load("contacts");
    client.get.mockClear();

    locales.setLocale("fr");
    await vi.waitFor(() => expect(store.translate("contacts", "fr", "actions.edit")).toBe("Modifier"));
    expect(client.get.mock.calls.map(([path]) => path)).toEqual(["/modules/contacts/translations/fr.json"]);
  });

  it("refreshLoaded refetches every loaded module and ignores a response from before the refresh", async () => {
    const { store, client, loader } = setup("en", { "sales/en": { "actions.edit": "Edit sales" } });
    let resolveStale: (messages: Record<string, string>) => void = () => {};
    client.get.mockImplementationOnce(() => new Promise((resolve) => (resolveStale = resolve)));
    const stale = loader.load("contacts");
    await loader.load("sales");
    client.get.mockClear();

    client.get.mockResolvedValueOnce({ "actions.edit": "Edit v2" });
    await loader.refreshLoaded();
    resolveStale({ "actions.edit": "Edit v1" });
    await stale;

    expect(store.translate("contacts", "en", "actions.edit")).toBe("Edit v2");
    expect(client.get.mock.calls.map(([path]) => path)).toEqual([
      "/modules/contacts/translations/en.json",
      "/modules/sales/translations/en.json",
    ]);
  });

  it("warns and retries on the next load after a failure other than 404", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { store, client, loader } = setup("en", {});
    client.get.mockRejectedValueOnce(new Error("network down"));

    await expect(loader.load("contacts")).resolves.toBeUndefined();
    expect(warn).toHaveBeenCalledWith("load contacts translations for en:", expect.any(Error));
    expect(store.translate("contacts", "en", "actions.edit")).toBe("actions.edit");

    client.get.mockResolvedValueOnce({ "actions.edit": "Edit" });
    await loader.load("contacts");
    expect(store.translate("contacts", "en", "actions.edit")).toBe("Edit");
  });
});
