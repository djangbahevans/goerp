import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { UploadHandle } from "./file-field-upload.js";
import { SignaturePad } from "./signature-pad.js";

vi.mock("@fontsource/caveat/400.css", () => ({}));

function fakeContext() {
  return {
    clearRect: vi.fn(),
    beginPath: vi.fn(),
    moveTo: vi.fn(),
    lineTo: vi.fn(),
    stroke: vi.fn(),
    fillText: vi.fn(),
    measureText: vi.fn(() => ({ width: 120 })),
    font: "",
    fillStyle: "",
    strokeStyle: "",
    textAlign: "",
    textBaseline: "",
  };
}

let ctx: ReturnType<typeof fakeContext>;
let fontFaces: unknown[];

function uploadReturning(fileId: string) {
  return vi.fn(
    (): UploadHandle => ({
      promise: Promise.resolve({ fileId, name: "signature.png", contentType: "image/png", sizeBytes: 3 }),
      abort: vi.fn(),
    }),
  );
}

beforeEach(() => {
  ctx = fakeContext();
  fontFaces = [{}];
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(
    () => ctx as unknown as CanvasRenderingContext2D,
  );
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((callback) =>
    callback(new Blob(["png"], { type: "image/png" })),
  );
  Object.defineProperty(document, "fonts", { configurable: true, value: { load: vi.fn(async () => fontFaces) } });
  URL.createObjectURL = vi.fn(() => "blob:signature");
  URL.revokeObjectURL = vi.fn();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

async function selectType() {
  fireEvent.click(screen.getByRole("radio", { name: "Type" }));
  await vi.waitFor(() => expect(document.fonts.load).toHaveBeenCalled());
}

describe("SignaturePad", () => {
  it("renders a drawing canvas when there is no value", () => {
    render(<SignaturePad value={null} onChange={() => {}} />);
    expect(screen.getByRole("img", { name: "Signature drawing area" }).tagName).toBe("CANVAS");
    expect(screen.queryByAltText("Signature captured")).toBeNull();
  });

  it("renders a drawing canvas for an empty string too, not a broken image", () => {
    render(<SignaturePad value="" onChange={() => {}} />);
    expect(screen.getByRole("img", { name: "Signature drawing area" }).tagName).toBe("CANVAS");
  });

  it("renders a data URL value as the captured image", () => {
    render(<SignaturePad value="data:image/png;base64,abc" onChange={() => {}} />);
    expect((screen.getByAltText("Signature captured") as HTMLImageElement).src).toBe("data:image/png;base64,abc");
  });

  it("renders a file id value through valueUrl", () => {
    render(<SignaturePad value="01jfile" valueUrl="https://cdn.test/sig.png" onChange={() => {}} />);
    expect((screen.getByAltText("Signature captured") as HTMLImageElement).src).toBe("https://cdn.test/sig.png");
  });

  it("clears the value when Clear is clicked", () => {
    const onChange = vi.fn();
    render(<SignaturePad value="data:image/png;base64,abc" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("disables the Clear button when disabled", () => {
    render(<SignaturePad value="data:image/png;base64,abc" onChange={() => {}} disabled />);
    expect(screen.getByRole("button", { name: "Clear" }).hasAttribute("disabled")).toBe(true);
  });

  it("names the whole control with ariaLabel, defaulting to 'Signature'", () => {
    const { rerender } = render(<SignaturePad value={null} onChange={() => {}} />);
    expect(screen.getByRole("group", { name: "Signature" })).toBeTruthy();
    rerender(<SignaturePad value={null} onChange={() => {}} ariaLabel="Approval signature" />);
    expect(screen.getByRole("group", { name: "Approval signature" })).toBeTruthy();
  });

  it("uploads a finished stroke and reports the file id", async () => {
    const onChange = vi.fn();
    const uploadFn = uploadReturning("01jdrawn");
    render(<SignaturePad value={null} onChange={onChange} uploadFn={uploadFn} />);
    const canvas = screen.getByRole("img", { name: "Signature drawing area" });
    fireEvent.pointerDown(canvas, { clientX: 1, clientY: 1 });
    fireEvent.pointerMove(canvas, { clientX: 20, clientY: 10 });
    fireEvent.pointerUp(canvas);

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith("01jdrawn"));
    expect(uploadFn).toHaveBeenCalledWith(expect.any(Blob), "signature.png", "attachments", expect.any(Function));
  });

  it("does not upload on a tap without a stroke", () => {
    const uploadFn = uploadReturning("01jblank");
    render(<SignaturePad value={null} onChange={() => {}} uploadFn={uploadFn} />);
    const canvas = screen.getByRole("img", { name: "Signature drawing area" });
    fireEvent.pointerDown(canvas);
    fireEvent.pointerUp(canvas);
    expect(uploadFn).not.toHaveBeenCalled();
  });

  it("shows a captured image from the local capture and announces it", async () => {
    const uploadFn = uploadReturning("01jdrawn");
    function Harness() {
      const [value, setValue] = useState<string | null>(null);
      return <SignaturePad value={value} onChange={setValue} uploadFn={uploadFn} />;
    }
    render(<Harness />);
    const canvas = screen.getByRole("img", { name: "Signature drawing area" });
    fireEvent.pointerDown(canvas);
    fireEvent.pointerMove(canvas, { clientX: 20, clientY: 10 });
    fireEvent.pointerUp(canvas);

    const img = (await screen.findByAltText("Signature captured")) as HTMLImageElement;
    expect(img.src).toBe("blob:signature");
    expect(screen.getByRole("status").textContent).toBe("Signature captured.");
  });

  it("offers a retry when the upload fails", async () => {
    const onChange = vi.fn();
    const uploadFn = vi
      .fn<() => UploadHandle>()
      .mockReturnValueOnce({ promise: Promise.reject(new Error("offline")), abort: vi.fn() })
      .mockReturnValueOnce(uploadReturning("01jretry")());
    render(<SignaturePad value={null} onChange={onChange} uploadFn={uploadFn} />);
    const canvas = screen.getByRole("img", { name: "Signature drawing area" });
    fireEvent.pointerDown(canvas);
    fireEvent.pointerMove(canvas, { clientX: 20, clientY: 10 });
    fireEvent.pointerUp(canvas);

    await screen.findByRole("alert");
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith("01jretry"));
  });

  describe("Type mode", () => {
    it("pre-fills the name from signerName, capped at 80 characters", async () => {
      render(<SignaturePad value={null} onChange={() => {}} signerName="Kofi Mensah" />);
      await selectType();
      const input = screen.getByLabelText("Type your full name") as HTMLInputElement;
      expect(input.value).toBe("Kofi Mensah");
      expect(input.maxLength).toBe(80);
      expect(screen.getByRole("img", { name: "Signature preview" }).tagName).toBe("CANVAS");
    });

    it("follows signerName until the user edits the name", async () => {
      const { rerender } = render(<SignaturePad value={null} onChange={() => {}} />);
      await selectType();
      rerender(<SignaturePad value={null} onChange={() => {}} signerName="Kofi Mensah" />);
      const input = screen.getByLabelText("Type your full name") as HTMLInputElement;
      expect(input.value).toBe("Kofi Mensah");
      fireEvent.change(input, { target: { value: "Kofi" } });
      rerender(<SignaturePad value={null} onChange={() => {}} signerName="Someone Else" />);
      expect(input.value).toBe("Kofi");
    });

    it("loads the font for the typed text, so every glyph is available when it is drawn", async () => {
      render(<SignaturePad value={null} onChange={() => {}} signerName="Ɔforiwaa" />);
      await selectType();
      await vi.waitFor(() => expect(document.fonts.load).toHaveBeenCalledWith("40px Caveat", "Ɔforiwaa"));
    });

    it("enables Adopt signature only once the font has loaded and a name is typed", async () => {
      render(<SignaturePad value={null} onChange={() => {}} />);
      fireEvent.click(screen.getByRole("radio", { name: "Type" }));
      const adopt = screen.getByRole("button", { name: "Adopt signature" });
      expect(adopt.hasAttribute("disabled")).toBe(true);

      fireEvent.change(screen.getByLabelText("Type your full name"), { target: { value: "  Ama  " } });
      await vi.waitFor(() => expect(adopt.hasAttribute("disabled")).toBe(false));

      fireEvent.change(screen.getByLabelText("Type your full name"), { target: { value: "   " } });
      expect(adopt.hasAttribute("disabled")).toBe(true);
    });

    it("keeps Adopt signature disabled and says why when the font cannot load", async () => {
      fontFaces = [];
      render(<SignaturePad value={null} onChange={() => {}} signerName="Ama" />);
      fireEvent.click(screen.getByRole("radio", { name: "Type" }));
      await screen.findByText(/signature font failed to load/i);
      expect(screen.getByRole("button", { name: "Adopt signature" }).hasAttribute("disabled")).toBe(true);
    });

    it("renders the trimmed name in Caveat at 40px, scaled down to fit", async () => {
      ctx.measureText.mockReturnValue({ width: 368 });
      render(<SignaturePad value={null} onChange={() => {}} signerName="  Ama  " />);
      await selectType();
      await vi.waitFor(() => expect(ctx.fillText).toHaveBeenCalledWith("Ama", 100, 40));
      expect(ctx.font).toBe("20px Caveat");
    });

    it("adopts the typed name through the same upload as a drawing", async () => {
      const onChange = vi.fn();
      const uploadFn = uploadReturning("01jtyped");
      render(<SignaturePad value={null} onChange={onChange} uploadFn={uploadFn} signerName="Ama" />);
      await selectType();
      const adopt = screen.getByRole("button", { name: "Adopt signature" });
      await vi.waitFor(() => expect(adopt.hasAttribute("disabled")).toBe(false));
      await act(async () => fireEvent.click(adopt));

      await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith("01jtyped"));
      expect(uploadFn).toHaveBeenCalledWith(expect.any(Blob), "signature.png", "attachments", expect.any(Function));
    });

    it("discards the typed name when switching to Draw and back", async () => {
      render(<SignaturePad value={null} onChange={() => {}} signerName="Ama" />);
      await selectType();
      fireEvent.change(screen.getByLabelText("Type your full name"), { target: { value: "Someone Else" } });
      fireEvent.click(screen.getByRole("radio", { name: "Draw" }));
      fireEvent.click(screen.getByRole("radio", { name: "Type" }));
      expect((screen.getByLabelText("Type your full name") as HTMLInputElement).value).toBe("Ama");
    });

    it("does not draw strokes in Type mode", async () => {
      render(<SignaturePad value={null} onChange={() => {}} />);
      await selectType();
      const canvas = screen.getByRole("img", { name: "Signature preview" });
      fireEvent.pointerDown(canvas);
      fireEvent.pointerMove(canvas, { clientX: 5, clientY: 5 });
      expect(ctx.moveTo).not.toHaveBeenCalled();
      expect(ctx.lineTo).not.toHaveBeenCalled();
    });
  });
});
