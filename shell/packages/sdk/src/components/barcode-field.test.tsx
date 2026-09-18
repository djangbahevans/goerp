import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BarcodeField, type BarcodeFormat } from "./barcode-field.js";

const { detectMock, barcodeDetectorCtorMock, getUserMediaMock } = vi.hoisted(() => ({
  detectMock: vi.fn(async (_image: unknown) => [] as { rawValue: string }[]),
  barcodeDetectorCtorMock: vi.fn(),
  getUserMediaMock: vi.fn(),
}));

vi.mock("barcode-detector/pure", () => ({
  BarcodeDetector: class {
    constructor(options: unknown) {
      barcodeDetectorCtorMock(options);
    }
    detect(image: unknown) {
      return detectMock(image);
    }
  },
}));

function fakeStream() {
  const track = { stop: vi.fn() };
  return { stream: { getTracks: () => [track] } as unknown as MediaStream, track };
}

beforeEach(() => {
  Object.defineProperty(navigator, "mediaDevices", {
    value: { getUserMedia: getUserMediaMock },
    configurable: true,
  });
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  getUserMediaMock.mockResolvedValue(fakeStream().stream);
  detectMock.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  getUserMediaMock.mockReset();
  detectMock.mockReset();
  barcodeDetectorCtorMock.mockReset();
});

describe("BarcodeField", () => {
  it("renders the manual-entry input with the current placeholder and a scan trigger", () => {
    render(<BarcodeField value="" onChange={vi.fn()} />);
    expect((screen.getByRole("textbox") as HTMLInputElement).placeholder).toBe("Scan or enter code");
    expect(screen.getByRole("button", { name: "Scan barcode" })).toBeTruthy();
  });

  it("typing in the manual input calls onChange directly, without touching the camera", () => {
    const onChange = vi.fn();
    render(<BarcodeField value="" onChange={onChange} />);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "012345" } });
    expect(onChange).toHaveBeenCalledWith("012345");
    expect(getUserMediaMock).not.toHaveBeenCalled();
  });

  it("does not request camera access on mount or focus — only on an explicit scan trigger", () => {
    render(<BarcodeField value="" onChange={vi.fn()} />);
    fireEvent.focus(screen.getByRole("textbox"));
    expect(getUserMediaMock).not.toHaveBeenCalled();
  });

  it("constructs the detector with the given formats, defaulting to the documented set", () => {
    render(<BarcodeField value="" onChange={vi.fn()} />);
    expect(barcodeDetectorCtorMock).toHaveBeenCalledWith({
      formats: ["qr_code", "ean_13", "ean_8", "upc_a", "upc_e", "code_128", "code_39"],
    });
  });

  it("opens the scanning overlay and requests camera access when the scan trigger is clicked", async () => {
    render(<BarcodeField value="" onChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
    await waitFor(() => expect(getUserMediaMock).toHaveBeenCalledWith({ video: { facingMode: "environment" } }));
    expect(screen.getByRole("dialog", { name: "Scan barcode" })).toBeTruthy();
  });

  it("writes the decoded value, announces success, and closes the overlay on a successful scan", async () => {
    vi.useFakeTimers();
    try {
      const onChange = vi.fn();
      detectMock.mockResolvedValue([{ rawValue: "9781234567897" }]);
      render(<BarcodeField value="" onChange={onChange} />);
      fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
      });
      expect(onChange).toHaveBeenCalledWith("9781234567897");
      expect(screen.queryByRole("dialog")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("calls onScan, not just onChange, on a successful decode — the only way a caller can tell a scan apart from typing", async () => {
    vi.useFakeTimers();
    try {
      const onChange = vi.fn();
      const onScan = vi.fn();
      detectMock.mockResolvedValue([{ rawValue: "9781234567897" }]);
      render(<BarcodeField value="" onChange={onChange} onScan={onScan} />);
      fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
      });
      expect(onScan).toHaveBeenCalledWith("9781234567897");
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not call onScan for manual typing", () => {
    const onScan = vi.fn();
    render(<BarcodeField value="" onChange={vi.fn()} onScan={onScan} />);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "012345" } });
    expect(onScan).not.toHaveBeenCalled();
  });

  it("falls back to the default formats when the constructor rejects a manifest-declared format", () => {
    barcodeDetectorCtorMock.mockImplementationOnce(() => {
      throw new TypeError('"not_a_real_format" is not a valid enum value of type BarcodeFormat.');
    });
    const badFormats = ["not_a_real_format"] as unknown as BarcodeFormat[];
    expect(() => render(<BarcodeField value="" onChange={vi.fn()} formats={badFormats} />)).not.toThrow();
    expect(barcodeDetectorCtorMock).toHaveBeenLastCalledWith({
      formats: ["qr_code", "ean_13", "ean_8", "upc_a", "upc_e", "code_128", "code_39"],
    });
  });

  it("ignores a detection with an empty rawValue instead of writing it and closing", async () => {
    vi.useFakeTimers();
    try {
      const onChange = vi.fn();
      detectMock.mockResolvedValue([{ rawValue: "" }]);
      render(<BarcodeField value="" onChange={onChange} />);
      fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
      });
      expect(onChange).not.toHaveBeenCalled();
      expect(screen.getByRole("dialog")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("disables the scan trigger immediately once scanning starts, preventing a second concurrent attempt", async () => {
    render(<BarcodeField value="" onChange={vi.fn()} />);
    const trigger = screen.getByRole("button", { name: "Scan barcode" });
    fireEvent.click(trigger);
    expect(trigger.hasAttribute("disabled")).toBe(true);
    await waitFor(() => expect(getUserMediaMock).toHaveBeenCalledTimes(1));
  });

  it("does not act on a decode that resolves after the user already cancelled", async () => {
    const onChange = vi.fn();
    // detect() only resolves once the test explicitly releases it, so
    // Cancel can run first while the poll's own detect() is in flight —
    // real timers, since the poll interval firing at all is what this
    // test needs, not precise control over exactly when.
    let resolveDetect: (value: { rawValue: string }[]) => void = () => {};
    detectMock.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveDetect = resolve;
        }),
    );
    render(<BarcodeField value="" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
    await waitFor(() => expect(detectMock).toHaveBeenCalled());
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await act(async () => {
      resolveDetect([{ rawValue: "9781234567897" }]);
      await Promise.resolve();
    });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not call onChange or close when detect() finds nothing yet", async () => {
    vi.useFakeTimers();
    try {
      const onChange = vi.fn();
      render(<BarcodeField value="" onChange={onChange} />);
      fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
      });
      expect(onChange).not.toHaveBeenCalled();
      expect(screen.getByRole("dialog")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the permission-denied message with a Close button, instead of trapping the user in the overlay", async () => {
    getUserMediaMock.mockRejectedValue(new DOMException("Permission denied", "NotAllowedError"));
    render(<BarcodeField value="" onChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
    expect(
      await screen.findByText("Camera access is needed to scan — you can still type the code below."),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Close" })).toBeTruthy();
  });

  it("shows a distinct message — not a permission-denial claim — when access is granted but the preview fails to start, and releases the stream", async () => {
    const { stream, track } = fakeStream();
    getUserMediaMock.mockResolvedValue(stream);
    HTMLMediaElement.prototype.play = vi.fn().mockRejectedValue(new Error("autoplay blocked"));
    render(<BarcodeField value="" onChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
    expect(
      await screen.findByText("The camera preview couldn't start — you can still type the code below."),
    ).toBeTruthy();
    expect(track.stop).toHaveBeenCalled();
  });

  it("shows a distinct message when decoding fails repeatedly, instead of polling forever with no feedback", async () => {
    vi.useFakeTimers();
    try {
      const { stream, track } = fakeStream();
      getUserMediaMock.mockResolvedValue(stream);
      detectMock.mockRejectedValue(new Error("wasm module failed to load"));
      render(<BarcodeField value="" onChange={vi.fn()} />);
      fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(300 * 10);
      });
      expect(screen.getByText("Scanning isn't working right now — you can still type the code below.")).toBeTruthy();
      expect(track.stop).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("closing the permission-denied message sends focus to the manual input, not the scan trigger", async () => {
    getUserMediaMock.mockRejectedValue(new DOMException("Permission denied", "NotAllowedError"));
    render(<BarcodeField value="" onChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
    fireEvent.click(await screen.findByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(screen.getByRole("textbox"));
  });

  it("cancelling an in-progress scan stops the camera stream and returns focus to the scan trigger", async () => {
    const { stream, track } = fakeStream();
    getUserMediaMock.mockResolvedValue(stream);
    render(<BarcodeField value="" onChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(track.stop).toHaveBeenCalled();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Scan barcode" }));
  });

  it("closes and stops the stream on Escape", async () => {
    const { stream, track } = fakeStream();
    getUserMediaMock.mockResolvedValue(stream);
    render(<BarcodeField value="" onChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
    await screen.findByRole("dialog");
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(track.stop).toHaveBeenCalled();
  });

  it("stops the camera stream on unmount", async () => {
    const { stream, track } = fakeStream();
    getUserMediaMock.mockResolvedValue(stream);
    const { unmount } = render(<BarcodeField value="" onChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Scan barcode" }));
    await waitFor(() => expect(getUserMediaMock).toHaveBeenCalled());
    unmount();
    expect(track.stop).toHaveBeenCalled();
  });

  it("shows a pending spinner and disables the input while isLookingUp is true", () => {
    render(<BarcodeField value="012345" onChange={vi.fn()} isLookingUp />);
    expect(screen.getByRole("textbox").hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: "Scan barcode" }).hasAttribute("disabled")).toBe(true);
  });

  it("renders an error message and marks the input invalid", () => {
    render(<BarcodeField value="" onChange={vi.fn()} error="Invalid barcode" />);
    expect(screen.getByRole("alert").textContent).toBe("Invalid barcode");
    expect(screen.getByRole("textbox").getAttribute("aria-invalid")).toBe("true");
  });

  it("disables the manual input and scan trigger when disabled", () => {
    render(<BarcodeField value="" onChange={vi.fn()} disabled />);
    expect(screen.getByRole("textbox").hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: "Scan barcode" }).hasAttribute("disabled")).toBe(true);
  });
});
