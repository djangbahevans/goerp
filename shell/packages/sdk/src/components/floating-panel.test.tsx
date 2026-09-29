import { act, cleanup, render } from "@testing-library/react";
import type { MutableRefObject, ReactNode } from "react";
import { useEffect, useRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useFloatingPanelLayer, useFloatingPanelPosition } from "./floating-panel.js";

afterEach(cleanup);

// jsdom's default getBoundingClientRect always returns an all-zero rect, so
// none of this hook's actual clamp/fit math is exercised without mocking it
// per element — this is what let a real matchTriggerWidth bug ship
// unnoticed through every existing consumer's own test suite. Stubbed at
// the prototype level, keyed by data-testid, so it's active before the
// hook's synchronous useLayoutEffect runs on initial mount.
function stubRectsByTestId(rects: Record<string, Partial<DOMRect>>): void {
  Element.prototype.getBoundingClientRect = function (this: HTMLElement) {
    const rect = rects[this.dataset.testid ?? ""];
    return { top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0, x: 0, y: 0, toJSON() {}, ...rect } as DOMRect;
  };
}

type Position = { top: number; left: number; width: number } | null;

function Harness({
  matchTriggerWidth,
  capturedRef,
}: {
  matchTriggerWidth: boolean;
  capturedRef: MutableRefObject<Position>;
}): ReactNode {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const position = useFloatingPanelPosition(true, containerRef, panelRef, matchTriggerWidth);
  useEffect(() => {
    capturedRef.current = position;
  });
  return (
    <div>
      <div ref={containerRef} data-testid="container" />
      <div ref={panelRef} data-testid="panel" />
    </div>
  );
}

function renderAndCapture(matchTriggerWidth: boolean): Position {
  const capturedRef: MutableRefObject<Position> = { current: null };
  render(<Harness matchTriggerWidth={matchTriggerWidth} capturedRef={capturedRef} />);
  return capturedRef.current;
}

describe("useFloatingPanelPosition", () => {
  it("matchTriggerWidth: clamps against the trigger's own (wider) width, not the panel's narrower natural pre-override measurement", () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 800 });
    Object.defineProperty(window, "innerHeight", { configurable: true, value: 600 });
    // A wide trigger (500px) sitting near the right edge of an 800px-wide
    // viewport. The panel's *natural* pre-width-override measurement (a
    // short-content row, before `width: containerRect.width` applies on the
    // caller's next render) is much narrower (200px) than the 500px it will
    // actually be forced to.
    stubRectsByTestId({
      container: { top: 100, bottom: 120, left: 700, width: 500 },
      panel: { height: 200, width: 200 },
    });

    // Correct: clamped against max(500, 200) = 500 → maxLeft = 800-500-8 =
    // 292 → left = 292, so the panel (actually 500px wide once rendered)
    // spans 292-792, fitting inside the 800px viewport. The pre-fix bug
    // clamped against the natural 200px measurement alone → maxLeft =
    // 800-200-8 = 592 → left = 592, so the actually-500px-wide panel would
    // span 592-1092, overflowing the viewport's right edge by 292px.
    expect(renderAndCapture(true)?.left).toBe(292);
  });

  it("!matchTriggerWidth: clamps against the panel's own natural measured width (ActionMenu/SelectMultiple/IconPicker's shape)", () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 800 });
    Object.defineProperty(window, "innerHeight", { configurable: true, value: 600 });
    stubRectsByTestId({
      container: { top: 100, bottom: 120, left: 700, width: 60 },
      panel: { height: 200, width: 220 },
    });

    // maxLeft = 800 - 220 - 8 = 572; containerRect.left (700) exceeds it.
    expect(renderAndCapture(false)?.left).toBe(572);
  });

  it("flips above the trigger when the panel wouldn't fit below", () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 800 });
    Object.defineProperty(window, "innerHeight", { configurable: true, value: 400 });
    stubRectsByTestId({
      container: { top: 350, bottom: 370, left: 50, width: 200 },
      panel: { height: 100, width: 200 },
    });

    // Below: 370 + 4 + 100 = 474 > 400 - 8 = 392, doesn't fit — flips above:
    // max(8, 350 - 4 - 100) = 246.
    expect(renderAndCapture(true)?.top).toBe(246);
  });

  it("re-places the panel when the trigger resizes while open", () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 800 });
    Object.defineProperty(window, "innerHeight", { configurable: true, value: 600 });
    let notify: () => void = () => {};
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(callback: () => void) {
          notify = callback;
        }
        observe() {}
        disconnect() {}
      },
    );
    stubRectsByTestId({ container: { top: 100, bottom: 120, left: 10, width: 300 }, panel: { height: 100 } });
    const capturedRef: MutableRefObject<Position> = { current: null };
    const { rerender } = render(<Harness matchTriggerWidth capturedRef={capturedRef} />);
    expect(capturedRef.current?.top).toBe(124);

    // A pill row above the input grows the trigger by 32px.
    stubRectsByTestId({ container: { top: 100, bottom: 152, left: 10, width: 300 }, panel: { height: 100 } });
    act(() => notify());
    rerender(<Harness matchTriggerWidth capturedRef={capturedRef} />);
    expect(capturedRef.current?.top).toBe(156);
    vi.unstubAllGlobals();
  });
});

function LayerHarness(): ReactNode {
  const anchorRef = useRef<HTMLButtonElement | null>(null);
  const layerClassName = useFloatingPanelLayer(anchorRef);
  return (
    <button ref={anchorRef} type="button" data-layer={layerClassName}>
      Open
    </button>
  );
}

describe("useFloatingPanelLayer", () => {
  it("stays on --z-dropdown for a trigger on the page", () => {
    const { getByRole } = render(<LayerHarness />);
    expect(getByRole("button").dataset.layer).toBe("z-(--z-dropdown)");
  });

  it.each(["dialog", "alertdialog"])("takes --z-modal for a trigger inside a %s", (role) => {
    const { getByRole } = render(
      <div role={role}>
        <LayerHarness />
      </div>,
    );
    expect(getByRole("button").dataset.layer).toBe("z-(--z-modal)");
  });
});
