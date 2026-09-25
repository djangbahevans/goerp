import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { type ReactNode, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EscapeLayer } from "./escape-layer.js";

afterEach(cleanup);

// Overlays open one after another in real use, so the inner layer mounts
// after the outer one and sits above it in the stack.
function Opener({ name }: { name: string }) {
  const [open, setOpen] = useState(false);
  return open ? (
    <Panel name={name} />
  ) : (
    <button type="button" onClick={() => setOpen(true)}>
      Open {name}
    </button>
  );
}

function Panel({ name, children }: { name: string; children?: ReactNode }) {
  const [open, setOpen] = useState(true);
  if (!open) return null;
  return (
    <EscapeLayer onEscape={() => setOpen(false)}>
      <div role="dialog" aria-label={name}>
        {children}
      </div>
    </EscapeLayer>
  );
}

describe("EscapeLayer", () => {
  it("closes only the topmost of two nested layers per Escape press", () => {
    render(
      <Panel name="outer">
        <Opener name="inner" />
      </Panel>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open inner" }));
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "inner" })).toBeNull();
    expect(screen.getByRole("dialog", { name: "outer" })).toBeTruthy();
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "outer" })).toBeNull();
  });

  it("shares one stack with a Radix dialog it's opened inside", () => {
    const onOpenChange = vi.fn();
    render(
      <DialogPrimitive.Root open onOpenChange={onOpenChange}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Content aria-describedby={undefined}>
            <DialogPrimitive.Title>Radix</DialogPrimitive.Title>
            <Opener name="dropdown" />
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open dropdown" }));
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "dropdown" })).toBeNull();
    expect(onOpenChange).not.toHaveBeenCalled();
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("stops the handled Escape before any other keydown handler sees it", () => {
    const inner = vi.fn();
    render(
      // biome-ignore lint/a11y/noStaticElementInteractions: a bare listener standing in for an ancestor's own Escape handler.
      <div onKeyDown={inner}>
        <Panel name="panel" />
      </div>,
    );
    const documentListener = vi.fn();
    document.addEventListener("keydown", documentListener);
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    document.removeEventListener("keydown", documentListener);
    expect(inner).not.toHaveBeenCalled();
    expect(documentListener).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("reports an outside pointer press without closing on its own", async () => {
    const onOutside = vi.fn();
    render(
      <div>
        <EscapeLayer onEscape={vi.fn()} onPointerDownOutside={onOutside}>
          <div role="dialog" aria-label="panel" />
        </EscapeLayer>
        <button type="button">Elsewhere</button>
      </div>,
    );
    // Radix starts listening for outside presses a tick after mount.
    await new Promise((resolve) => setTimeout(resolve, 0));
    fireEvent.pointerDown(screen.getByRole("button", { name: "Elsewhere" }));
    expect(onOutside).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("dialog", { name: "panel" })).toBeTruthy();
  });

  it("leaves outside clicks to the overlay's own handling", async () => {
    render(
      <div>
        <Panel name="panel" />
        <button type="button">Elsewhere</button>
      </div>,
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
    fireEvent.pointerDown(screen.getByRole("button", { name: "Elsewhere" }));
    fireEvent.focus(screen.getByRole("button", { name: "Elsewhere" }));
    expect(screen.getByRole("dialog", { name: "panel" })).toBeTruthy();
  });
});
