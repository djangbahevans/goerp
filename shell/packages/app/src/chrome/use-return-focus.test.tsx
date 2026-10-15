import { cleanup, render, renderHook } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { useReturnFocus } from "./use-return-focus.js";

afterEach(cleanup);

function closeEvent(): Event {
  return new Event("focusout", { cancelable: true });
}

it("returns focus to the element focused when the dialog opened", () => {
  render(
    <main tabIndex={-1}>
      <button type="button">Open</button>
    </main>,
  );
  const opener = document.querySelector("button") as HTMLButtonElement;
  opener.focus();
  const { result } = renderHook(() => useReturnFocus());

  result.current.onOpenAutoFocus();
  opener.blur();
  const event = closeEvent();
  result.current.onCloseAutoFocus(event);

  expect(event.defaultPrevented).toBe(true);
  expect(document.activeElement).toBe(opener);
});

it("falls back to <main> when the opener has left the document", () => {
  const { container } = render(
    <main tabIndex={-1}>
      <button type="button">Open</button>
    </main>,
  );
  const opener = container.querySelector("button") as HTMLButtonElement;
  opener.focus();
  const { result } = renderHook(() => useReturnFocus());

  result.current.onOpenAutoFocus();
  opener.remove();
  result.current.onCloseAutoFocus(closeEvent());

  expect(document.activeElement).toBe(container.querySelector("main"));
});
