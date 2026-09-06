import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AlertDialog } from "./alert-dialog.js";

afterEach(cleanup);

describe("AlertDialog", () => {
  it("renders nothing when closed", () => {
    render(
      <AlertDialog open={false} title="Archive Contact" description="..." onConfirm={vi.fn()} onCancel={vi.fn()} />,
    );
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("renders title/description and fires onConfirm/onCancel with no input configured", () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    render(
      <AlertDialog
        open
        title="Archive Contact"
        description="This contact will be archived."
        confirmLabel="Archive"
        confirmVariant="danger"
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );
    expect(screen.getByText("Archive Contact")).toBeTruthy();
    expect(screen.getByText("This contact will be archived.")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Archive" }));
    expect(onConfirm).toHaveBeenCalledWith(undefined);

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalled();
  });

  it("collects a text input value and passes it to onConfirm", () => {
    const onConfirm = vi.fn();
    render(
      <AlertDialog
        open
        title="Cancel Order"
        description="This order will be cancelled."
        confirmLabel="Cancel Order"
        input={{ label: "Reason for cancellation", type: "text", placeholder: "Optional" }}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByPlaceholderText("Optional"), { target: { value: "Customer request" } });
    fireEvent.click(screen.getByRole("button", { name: "Cancel Order" }));
    expect(onConfirm).toHaveBeenCalledWith("Customer request");
  });

  it("disables confirm until a required input is filled", () => {
    render(
      <AlertDialog
        open
        title="Cancel Order"
        description="..."
        input={{ label: "Reason", type: "text", required: true }}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    const confirmButton = screen.getByRole("button", { name: "Confirm" });
    expect(confirmButton.hasAttribute("disabled")).toBe(true);

    fireEvent.change(screen.getByLabelText("Reason"), { target: { value: "x" } });
    expect(confirmButton.hasAttribute("disabled")).toBe(false);
  });

  it("collects a select input value", () => {
    const onConfirm = vi.fn();
    render(
      <AlertDialog
        open
        title="Change status"
        description="..."
        input={{
          label: "Status",
          type: "select",
          options: [
            { value: "won", label: "Won" },
            { value: "lost", label: "Lost" },
          ],
        }}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "lost" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    expect(onConfirm).toHaveBeenCalledWith("lost");
  });

  it("resets the collected input each time the dialog reopens", () => {
    const { rerender } = render(
      <AlertDialog
        open
        title="Cancel Order"
        description="..."
        input={{ label: "Reason", type: "text" }}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByLabelText("Reason"), { target: { value: "typed text" } });

    rerender(
      <AlertDialog
        open={false}
        title="Cancel Order"
        description="..."
        input={{ label: "Reason", type: "text" }}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    rerender(
      <AlertDialog
        open
        title="Cancel Order"
        description="..."
        input={{ label: "Reason", type: "text" }}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect((screen.getByLabelText("Reason") as HTMLInputElement).value).toBe("");
  });
});
