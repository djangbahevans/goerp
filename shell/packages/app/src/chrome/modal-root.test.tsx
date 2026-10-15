import { modalManager } from "@goerp/sdk/realtime";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { ModalRoot } from "./modal-root.js";

afterEach(() => {
  cleanup();
  while (modalManager.getStack().length > 0) modalManager.closeTop();
});

it("subscribes to the modal stack and removes only the top entry on close", () => {
  render(<ModalRoot renderForm={(entry) => <output>{entry.config.resource}</output>} />);

  act(() => modalManager.open({ resource: "sales.invoice" }));
  act(() => modalManager.open({ resource: "sales.order" }));
  expect(screen.getAllByRole("status").map((node) => node.textContent)).toEqual(["sales.invoice", "sales.order"]);

  act(() => modalManager.closeTop());
  expect(screen.queryByText("sales.order")).toBeNull();
  expect(screen.getByText("sales.invoice")).toBeTruthy();
});
