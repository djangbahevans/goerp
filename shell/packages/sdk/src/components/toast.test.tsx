import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastBus } from "../notifications/toast.js";
import { Toast } from "./toast.js";

afterEach(cleanup);

describe("Toast", () => {
  it("renders nothing when there are no toasts", () => {
    const bus = new ToastBus();
    const { container } = render(<Toast bus={bus} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders a toast pushed before mount, and updates live as toasts change", async () => {
    const bus = new ToastBus();
    bus.success("Contact created");
    render(<Toast bus={bus} />);
    expect(screen.getByText("Contact created")).toBeTruthy();

    bus.error("Failed to save");
    expect(await screen.findByText("Failed to save")).toBeTruthy();
  });

  it("gives an error toast role=alert and other variants role=status", () => {
    const bus = new ToastBus();
    bus.error("Failed to save");
    bus.success("Saved");
    render(<Toast bus={bus} />);

    expect(screen.getByRole("alert").textContent).toContain("Failed to save");
    expect(screen.getByRole("status").textContent).toContain("Saved");
  });

  it("renders an action button and fires its onClick", () => {
    const bus = new ToastBus();
    const onClick = vi.fn();
    bus.error("Failed to send email", { action: { label: "Retry", onClick } });
    render(<Toast bus={bus} />);

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onClick).toHaveBeenCalled();
  });

  it("dismisses a toast via its own dismiss button", () => {
    const bus = new ToastBus();
    bus.success("Saved");
    render(<Toast bus={bus} />);

    fireEvent.click(screen.getByRole("button", { name: "Dismiss: Saved" }));

    expect(screen.queryByText("Saved")).toBeNull();
    expect(bus.getToasts()).toHaveLength(0);
  });

  it("renders no dismiss button for a loading toast — only the caller resolves it", () => {
    const bus = new ToastBus();
    bus.loading("Generating invoice...");
    render(<Toast bus={bus} />);

    expect(screen.queryByRole("button", { name: /Dismiss/ })).toBeNull();
  });
});
