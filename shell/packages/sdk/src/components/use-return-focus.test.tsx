import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it } from "vitest";
import { useReturnFocus } from "./use-return-focus.js";

afterEach(cleanup);

// A trigger-less dialog whose field takes focus with autoFocus, the case in
// which Radix fires no onOpenAutoFocus.
function Harness({ returnFocusTo, keepOpeners = true }: { returnFocusTo?: HTMLElement | null; keepOpeners?: boolean }) {
  const [open, setOpen] = useState(false);
  const restoreFocus = useReturnFocus(open, returnFocusTo);
  return (
    <main tabIndex={-1}>
      {(keepOpeners || !open) && (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            First
          </button>
          <button type="button" onClick={() => setOpen(true)}>
            Second
          </button>
        </>
      )}
      <button type="button">Elsewhere</button>
      <DialogPrimitive.Root open={open} onOpenChange={setOpen}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Content aria-describedby={undefined} onCloseAutoFocus={restoreFocus}>
            <DialogPrimitive.Title>Dialog</DialogPrimitive.Title>
            {/* biome-ignore lint/a11y/noAutofocus: reproduces a dialog that focuses its field on open. */}
            <input aria-label="Code" autoFocus />
            <button type="button" onClick={() => setOpen(false)}>
              Close
            </button>
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
    </main>
  );
}

function openFrom(name: string) {
  const opener = screen.getByRole("button", { name });
  opener.focus();
  fireEvent.click(opener);
  return opener;
}

function close() {
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
}

it("returns focus to the opener even when a field in the dialog autofocuses", async () => {
  render(<Harness />);
  const opener = openFrom("First");
  await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Code")));

  close();

  await waitFor(() => expect(document.activeElement).toBe(opener));
});

it("returns focus to each open's own opener", async () => {
  render(<Harness />);
  openFrom("First");
  close();
  await waitFor(() => expect(document.activeElement?.textContent).toBe("First"));

  const second = openFrom("Second");
  close();

  await waitFor(() => expect(document.activeElement).toBe(second));
});

it("falls back to <main> when the opener has left the page and focus is lost", async () => {
  render(<Harness keepOpeners={false} />);
  openFrom("First");
  await waitFor(() => expect(screen.queryByRole("button", { name: "First" })).toBeNull());

  close();

  await waitFor(() => expect(document.activeElement?.tagName).toBe("MAIN"));
});

it("focuses returnFocusTo instead of the opener, and nothing when it is null", async () => {
  const { rerender } = render(<Harness />);
  const elsewhere = screen.getByRole("button", { name: "Elsewhere" });
  rerender(<Harness returnFocusTo={elsewhere} />);
  openFrom("First");
  close();
  await waitFor(() => expect(document.activeElement).toBe(elsewhere));

  rerender(<Harness returnFocusTo={null} />);
  openFrom("First");
  close();
  await new Promise((resolve) => setTimeout(resolve, 10));

  expect(document.activeElement).toBe(document.body);
});
