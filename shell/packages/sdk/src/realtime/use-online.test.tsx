import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { online, useOnline } from "./index.js";

function Probe() {
  return <p>{useOnline() ? "online" : "offline"}</p>;
}

afterEach(() => {
  act(() => {
    window.dispatchEvent(new Event("online"));
  });
  cleanup();
});

describe("useOnline", () => {
  it("reports the same value as online and updates with the browser's events", () => {
    render(<Probe />);
    const seen: boolean[] = [];
    const unsubscribe = online.subscribe((value) => seen.push(value));
    expect(screen.getByText("online")).toBeTruthy();

    act(() => {
      window.dispatchEvent(new Event("offline"));
    });
    expect(screen.getByText("offline")).toBeTruthy();
    expect(online.get()).toBe(false);

    act(() => {
      window.dispatchEvent(new Event("online"));
    });
    expect(screen.getByText("online")).toBeTruthy();
    expect(online.get()).toBe(true);
    expect(seen).toEqual([false, true]);
    unsubscribe();
  });
});
