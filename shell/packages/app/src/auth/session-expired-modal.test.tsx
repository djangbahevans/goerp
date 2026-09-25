import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SessionExpiredModal } from "./session-expired-modal.js";

afterEach(cleanup);

describe("SessionExpiredModal", () => {
  it("shows the lock icon, heading, message and a single sign-in action", async () => {
    render(<SessionExpiredModal onSignIn={vi.fn()} />);

    const dialog = screen.getByRole("alertdialog", { name: "Your session has expired" });
    expect(screen.getByText("Please sign in again to continue.")).toBeTruthy();
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Sign in again"]);
    await waitFor(() => expect(dialog.querySelector(".lucide-lock")).not.toBeNull());
  });

  it("calls onSignIn from the button", () => {
    const onSignIn = vi.fn();
    render(<SessionExpiredModal onSignIn={onSignIn} />);

    fireEvent.click(screen.getByRole("button", { name: "Sign in again" }));

    expect(onSignIn).toHaveBeenCalledOnce();
  });

  it("stays open on Escape", () => {
    render(<SessionExpiredModal onSignIn={vi.fn()} />);

    fireEvent.keyDown(screen.getByRole("alertdialog"), { key: "Escape" });

    expect(screen.getByRole("alertdialog")).toBeTruthy();
  });
});
