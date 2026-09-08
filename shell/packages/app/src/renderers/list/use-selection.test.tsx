import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useSelection } from "./use-selection.js";

describe("useSelection", () => {
  it("toggles a single id in and out of the selection", () => {
    const { result } = renderHook(() => useSelection());

    act(() => result.current.toggle("1"));
    expect(result.current.isSelected("1")).toBe(true);
    expect([...result.current.selectedIds]).toEqual(["1"]);

    act(() => result.current.toggle("1"));
    expect(result.current.isSelected("1")).toBe(false);
    expect(result.current.selectedIds.size).toBe(0);
  });

  it("toggleAll selects every given id when any is unselected", () => {
    const { result } = renderHook(() => useSelection());

    act(() => result.current.toggle("1"));
    act(() => result.current.toggleAll(["1", "2", "3"]));

    expect([...result.current.selectedIds].sort()).toEqual(["1", "2", "3"]);
  });

  it("toggleAll clears the selection once every given id is already selected", () => {
    const { result } = renderHook(() => useSelection());

    act(() => result.current.toggleAll(["1", "2"]));
    act(() => result.current.toggleAll(["1", "2"]));

    expect(result.current.selectedIds.size).toBe(0);
  });

  it("clear empties the selection", () => {
    const { result } = renderHook(() => useSelection());

    act(() => result.current.toggleAll(["1", "2"]));
    act(() => result.current.clear());

    expect(result.current.selectedIds.size).toBe(0);
  });
});
