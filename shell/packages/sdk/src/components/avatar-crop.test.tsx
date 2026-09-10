import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AvatarCrop } from "./avatar-crop.js";

afterEach(cleanup);

function fakeImageFile(): File {
  return new File([new Uint8Array(100)], "me.png", { type: "image/png" });
}

// jsdom doesn't implement HTMLCanvasElement.getContext (no `canvas` npm
// package installed) — actual crop drawing/export is verified via a real
// browser (Storybook + Playwright), not here.
describe("AvatarCrop", () => {
  it("renders the crop canvas, zoom slider, and both actions", () => {
    render(<AvatarCrop file={fakeImageFile()} onApply={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole("slider", { name: "Zoom" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Apply" })).toBeTruthy();
  });

  it("calls onCancel when Cancel is clicked", () => {
    const onCancel = vi.fn();
    render(<AvatarCrop file={fakeImageFile()} onApply={vi.fn()} onCancel={onCancel} />);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalled();
  });

  it("the zoom slider is bounded 1x-4x", () => {
    render(<AvatarCrop file={fakeImageFile()} onApply={vi.fn()} onCancel={vi.fn()} />);
    const slider = screen.getByRole("slider", { name: "Zoom" }) as HTMLInputElement;
    expect(slider.min).toBe("1");
    expect(slider.max).toBe("4");
  });

  it("disables both actions and the slider when disabled", () => {
    render(<AvatarCrop file={fakeImageFile()} onApply={vi.fn()} onCancel={vi.fn()} disabled />);
    expect((screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Apply" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("slider", { name: "Zoom" }) as HTMLInputElement).disabled).toBe(true);
  });
});
