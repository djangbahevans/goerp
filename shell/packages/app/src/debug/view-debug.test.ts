import type { APIExchange } from "@goerp/sdk";
import { describe, expect, it, vi } from "vitest";
import { type ConditionRecord, isViewDebugActive, ViewDebugRecorder } from "./view-debug.js";

function fakeObserver() {
  const listeners = new Set<(exchange: APIExchange) => void>();
  return {
    observeResponses: vi.fn((listener: (exchange: APIExchange) => void) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    }),
    emit: (exchange: APIExchange) => {
      for (const listener of listeners) listener(exchange);
    },
    listenerCount: () => listeners.size,
  };
}

const flush = () => new Promise<void>((resolve) => queueMicrotask(resolve));

const condition = (overrides: Partial<ConditionRecord> = {}): ConditionRecord => ({
  scope: "contacts.contact_form",
  location: 'field "vat" condition',
  expression: "record.country = 'GH'",
  kind: "visible",
  recordId: null,
  result: true,
  error: null,
  ...overrides,
});

const exchange = (url: string): APIExchange => ({ method: "GET", url, status: 200, body: { url } });

describe("isViewDebugActive", () => {
  it("needs both a development engine and debug=views", () => {
    expect(isViewDebugActive("development", { debug: "views" })).toBe(true);
    expect(isViewDebugActive("production", { debug: "views" })).toBe(false);
    expect(isViewDebugActive(null, { debug: "views" })).toBe(false);
    expect(isViewDebugActive("development", {})).toBe(false);
    expect(isViewDebugActive("development", { debug: "other" })).toBe(false);
  });
});

describe("ViewDebugRecorder", () => {
  it("records nothing while inactive", async () => {
    const observer = fakeObserver();
    const recorder = new ViewDebugRecorder(observer);

    recorder.recordCondition(condition());
    await flush();

    expect(observer.observeResponses).not.toHaveBeenCalled();
    expect(recorder.getSnapshot()).toEqual({ conditions: [], exchanges: [] });
  });

  it("records responses and conditions while active and notifies after the current task", async () => {
    const observer = fakeObserver();
    const recorder = new ViewDebugRecorder(observer);
    const listener = vi.fn();
    recorder.subscribe(listener);

    recorder.sync(true, "/_m/contacts");
    observer.emit(exchange("/contacts"));
    recorder.recordCondition(condition());
    expect(listener).not.toHaveBeenCalled();
    await flush();

    expect(listener).toHaveBeenCalledTimes(1);
    expect(recorder.getSnapshot()).toEqual({ conditions: [condition()], exchanges: [exchange("/contacts")] });
  });

  it("keeps the latest result per condition and skips unchanged re-evaluations", async () => {
    const recorder = new ViewDebugRecorder(fakeObserver());
    recorder.sync(true, "/_m/contacts");
    recorder.recordCondition(condition());
    await flush();
    const listener = vi.fn();
    recorder.subscribe(listener);

    recorder.recordCondition(condition());
    await flush();
    expect(listener).not.toHaveBeenCalled();

    recorder.recordCondition(condition({ result: false }));
    recorder.recordCondition(condition({ recordId: "c2" }));
    await flush();
    expect(recorder.getSnapshot().conditions).toEqual([condition({ result: false }), condition({ recordId: "c2" })]);
  });

  it("starts over on a new path but not on a repeat sync of the same one", async () => {
    const observer = fakeObserver();
    const recorder = new ViewDebugRecorder(observer);
    recorder.sync(true, "/_m/contacts");
    observer.emit(exchange("/contacts"));

    recorder.sync(true, "/_m/contacts");
    await flush();
    expect(recorder.getSnapshot().exchanges).toHaveLength(1);

    recorder.sync(true, "/_m/contacts/1");
    await flush();
    expect(recorder.getSnapshot().exchanges).toEqual([]);
    expect(observer.listenerCount()).toBe(1);
  });

  it("stops observing and clears its entries when deactivated", async () => {
    const observer = fakeObserver();
    const recorder = new ViewDebugRecorder(observer);
    recorder.sync(true, "/_m/contacts");
    observer.emit(exchange("/contacts"));

    recorder.sync(false, "/_m/contacts");
    observer.emit(exchange("/contacts/1"));
    await flush();

    expect(observer.listenerCount()).toBe(0);
    expect(recorder.active).toBe(false);
    expect(recorder.getSnapshot()).toEqual({ conditions: [], exchanges: [] });
  });

  it("keeps only the most recent 50 responses", async () => {
    const observer = fakeObserver();
    const recorder = new ViewDebugRecorder(observer);
    recorder.sync(true, "/_m/contacts");
    for (let page = 0; page < 60; page++) observer.emit(exchange(`/contacts?page=${page}`));
    await flush();

    const urls = recorder.getSnapshot().exchanges.map((e) => e.url);
    expect(urls).toHaveLength(50);
    expect(urls[0]).toBe("/contacts?page=10");
    expect(urls.at(-1)).toBe("/contacts?page=59");
  });

  it("stops adding conditions past 500 but keeps updating recorded ones", async () => {
    const recorder = new ViewDebugRecorder(fakeObserver());
    recorder.sync(true, "/_m/contacts");
    for (let row = 0; row < 510; row++) recorder.recordCondition(condition({ recordId: `c${row}` }));
    recorder.recordCondition(condition({ recordId: "c0", result: false }));
    await flush();

    const conditions = recorder.getSnapshot().conditions;
    expect(conditions).toHaveLength(500);
    expect(conditions[0]).toEqual(condition({ recordId: "c0", result: false }));
    expect(conditions.some((c) => c.recordId === "c500")).toBe(false);
  });

  it("stops adding conditions past 500 but keeps updating recorded ones", async () => {
    const recorder = new ViewDebugRecorder(fakeObserver());
    recorder.sync(true, "/_m/contacts");
    for (let row = 0; row < 510; row++) recorder.recordCondition(condition({ recordId: `c${row}` }));
    recorder.recordCondition(condition({ recordId: "c0", result: false }));
    await flush();

    const conditions = recorder.getSnapshot().conditions;
    expect(conditions).toHaveLength(500);
    expect(conditions[0]).toEqual(condition({ recordId: "c0", result: false }));
    expect(conditions.some((c) => c.recordId === "c500")).toBe(false);
  });
});
