import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
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

  it("triggers onCancel when Escape is pressed", async () => {
    const onCancel = vi.fn();
    render(<AlertDialog open title="Archive Contact" description="..." onConfirm={vi.fn()} onCancel={onCancel} />);
    fireEvent.keyDown(screen.getByRole("alertdialog"), { key: "Escape" });
    await vi.waitFor(() => expect(onCancel).toHaveBeenCalled());
  });

  it("does not dismiss when the overlay backdrop is clicked", () => {
    const onCancel = vi.fn();
    render(<AlertDialog open title="Archive Contact" description="..." onConfirm={vi.fn()} onCancel={onCancel} />);
    // The overlay backdrop is everything outside the dialog panel, e.g. body.
    fireEvent.pointerDown(document.body);
    expect(onCancel).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toBeTruthy();
  });

  it("auto-focuses the Cancel button when opened", async () => {
    render(
      <AlertDialog
        open
        title="Archive Contact"
        description="..."
        confirmLabel="Archive"
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    await vi.waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Cancel" })));
  });

  it("returns focus to the triggering element on close", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            Archive
          </button>
          <AlertDialog
            open={open}
            title="Archive Contact"
            description="..."
            onConfirm={vi.fn()}
            onCancel={() => setOpen(false)}
          />
        </>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Archive" });
    trigger.focus();
    fireEvent.click(trigger);
    await vi.waitFor(() => expect(screen.getByRole("alertdialog")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await vi.waitFor(() => expect(document.activeElement).toBe(trigger));
  });
});
