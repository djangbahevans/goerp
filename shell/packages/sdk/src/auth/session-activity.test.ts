import { afterEach, describe, expect, it, vi } from "vitest";
import { SessionActivity } from "./session-activity.js";

afterEach(() => vi.unstubAllGlobals());

describe("session activity", () => {
  it("keeps input received during refresh pending for the next cycle", () => {
    const activity = new SessionActivity();
    activity.record();
    const beforeRefresh = activity.snapshot();
    activity.record();
    activity.acknowledge(beforeRefresh);
    expect(activity.hasActivity()).toBe(true);
    activity.acknowledge();
    expect(activity.hasActivity()).toBe(false);
  });

  it("accepts trusted visible input and removes listeners when stopped", () => {
    const target = new EventTarget();
    const doc = {
      visibilityState: "visible",
      addEventListener: target.addEventListener.bind(target),
      removeEventListener: target.removeEventListener.bind(target),
    };
    vi.stubGlobal("document", doc);
    const activity = new SessionActivity();
    activity.start();
    activity.start();

    const trusted = (name: string) => {
      const event = new Event(name);
      Object.defineProperty(event, "isTrusted", { value: true });
      target.dispatchEvent(event);
    };
    target.dispatchEvent(new Event("keydown"));
    trusted("visibilitychange");
    trusted("focus");
    expect(activity.hasActivity()).toBe(false);

    for (const name of ["pointerdown", "pointermove", "keydown", "touchstart", "wheel"]) {
      trusted(name);
      expect(activity.hasActivity()).toBe(true);
      activity.acknowledge();
    }

    doc.visibilityState = "hidden";
    trusted("keydown");
    doc.visibilityState = "visible";
    expect(activity.hasActivity()).toBe(false);
    activity.stop();
    trusted("pointerdown");
    expect(activity.hasActivity()).toBe(false);
  });
});
