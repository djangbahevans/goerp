import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { FileValue } from "./file-field.js";
import { FileField } from "./file-field.js";
import type { UploadResult } from "./file-field-upload.js";
import { UploadError } from "./file-field-upload.js";

afterEach(cleanup);

function fakeFile(name = "report.pdf", type = "application/pdf", sizeBytes = 1024): File {
  return new File([new Uint8Array(sizeBytes)], name, { type });
}

// Deterministic fake: resolves/rejects synchronously-ish (microtask), with
// a single progress tick, so tests don't need to fake XHR internals.
function fakeUploadFn(result: UploadResult | Error) {
  const abort = vi.fn();
  return {
    fn: vi.fn((_file: File | Blob, _filename: string, _purpose: string, onProgress: (percent: number) => void) => {
      onProgress(50);
      const promise = result instanceof Error ? Promise.reject(result) : Promise.resolve<UploadResult>(result);
      return { promise, abort };
    }),
    abort,
  };
}

describe("FileField", () => {
  it("shows the empty capture area with no value", () => {
    render(<FileField value={null} onChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Browse files" })).toBeTruthy();
  });

  it("client-side rejects an oversized file before any upload call", () => {
    const { fn } = fakeUploadFn({ fileId: "1", name: "a", contentType: "a", sizeBytes: 1 });
    const onChange = vi.fn();
    render(<FileField value={null} onChange={onChange} maxFileSizeMb={1} uploadFn={fn} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const big = fakeFile("big.pdf", "application/pdf", 2 * 1024 * 1024);
    fireEvent.change(input, { target: { files: [big] } });
    expect(screen.getByRole("alert").textContent).toContain("exceeds");
    expect(fn).not.toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("multiple: reports every rejected file's own reason, not just the last one", () => {
    const { fn } = fakeUploadFn({ fileId: "1", name: "a", contentType: "a", sizeBytes: 1 });
    render(<FileField value={null} onChange={vi.fn()} multiple maxFileSizeMb={1} accept=".pdf" uploadFn={fn} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const tooBig = fakeFile("big.pdf", "application/pdf", 2 * 1024 * 1024);
    const wrongType = fakeFile("script.exe", "application/octet-stream", 100);
    fireEvent.change(input, { target: { files: [tooBig, wrongType] } });
    const message = screen.getByRole("alert").textContent ?? "";
    expect(message).toContain("big.pdf");
    expect(message).toContain("script.exe");
  });

  it("uploads a selected file and reports the completed FileValue", async () => {
    const result: UploadResult = { fileId: "f1", name: "report.pdf", contentType: "application/pdf", sizeBytes: 1024 };
    const { fn } = fakeUploadFn(result);
    const onChange = vi.fn();
    render(<FileField value={null} onChange={onChange} uploadFn={fn} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [fakeFile()] } });

    expect(screen.getByRole("progressbar")).toBeTruthy();
    await waitFor(() =>
      expect(onChange).toHaveBeenCalledWith({
        fileId: "f1",
        name: "report.pdf",
        contentType: "application/pdf",
        sizeBytes: 1024,
      }),
    );
  });

  it("shows an inline error and a retry button when the upload fails", async () => {
    const { fn } = fakeUploadFn(new UploadError(415));
    render(<FileField value={null} onChange={vi.fn()} uploadFn={fn} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [fakeFile()] } });

    const retry = await screen.findByRole("button", { name: "Try again" });
    expect(screen.getByRole("alert").textContent).toBe("File type not allowed.");
    expect(fn).toHaveBeenCalledTimes(1);

    fireEvent.click(retry);
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("removes a completed single file back to null", () => {
    const onChange = vi.fn();
    const value = { fileId: "f1", name: "report.pdf", contentType: "application/pdf", sizeBytes: 1024 };
    render(<FileField value={value} onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove report.pdf" }));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("multiple: keeps the capture area visible alongside existing chips", () => {
    const value = [{ fileId: "f1", name: "a.pdf", contentType: "application/pdf", sizeBytes: 100 }];
    render(<FileField value={value} onChange={vi.fn()} multiple />);
    expect(screen.getByRole("button", { name: "Browse files" })).toBeTruthy();
    expect(screen.getByText("a.pdf")).toBeTruthy();
  });

  it("multiple: removing one chip keeps the others", () => {
    const onChange = vi.fn();
    const value = [
      { fileId: "f1", name: "a.pdf", contentType: "application/pdf", sizeBytes: 100 },
      { fileId: "f2", name: "b.pdf", contentType: "application/pdf", sizeBytes: 200 },
    ];
    render(<FileField value={value} onChange={onChange} multiple />);
    fireEvent.click(screen.getByRole("button", { name: "Remove a.pdf" }));
    expect(onChange).toHaveBeenCalledWith([value[1]]);
  });

  it("multiple: two concurrent uploads resolving out of order both end up in the final value", async () => {
    const resolvers = new Map<string, (result: UploadResult) => void>();
    const fn = vi.fn(
      (_file: File | Blob, filename: string, _purpose: string, onProgress: (percent: number) => void) => {
        onProgress(50);
        const promise = new Promise<UploadResult>((resolve) => {
          resolvers.set(filename, resolve);
        });
        return { promise, abort: vi.fn() };
      },
    );

    function ControlledMultiField() {
      const [value, setValue] = useState<FileValue[]>([]);
      return (
        <FileField
          value={value}
          onChange={(next) => setValue(Array.isArray(next) ? next : [])}
          multiple
          uploadFn={fn}
        />
      );
    }
    render(<ControlledMultiField />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [fakeFile("a.pdf"), fakeFile("b.pdf")] } });

    // b.pdf resolves first even though a.pdf was selected first — the
    // stale-closure bug this regression-tests had the second resolution's
    // onChange overwrite the first's, silently dropping a.pdf.
    resolvers.get("b.pdf")?.({ fileId: "b.pdf", name: "b.pdf", contentType: "application/pdf", sizeBytes: 100 });
    await waitFor(() => expect(screen.getByText("b.pdf")).toBeTruthy());
    resolvers.get("a.pdf")?.({ fileId: "a.pdf", name: "a.pdf", contentType: "application/pdf", sizeBytes: 100 });
    await waitFor(() => expect(screen.getByText("a.pdf")).toBeTruthy());
    expect(screen.getByText("b.pdf")).toBeTruthy();
  });

  it("single-value: the file input is disabled once an upload starts, so a second selection can't race the first", () => {
    const { fn } = fakeUploadFn({ fileId: "f1", name: "a.pdf", contentType: "application/pdf", sizeBytes: 100 });
    render(<FileField value={null} onChange={vi.fn()} uploadFn={fn} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    expect(input.disabled).toBe(false);
    fireEvent.change(input, { target: { files: [fakeFile()] } });
    expect(input.disabled).toBe(true);
  });

  it("avatar variant shows the crop step instead of uploading immediately", () => {
    const { fn } = fakeUploadFn({ fileId: "f1", name: "a.png", contentType: "image/png", sizeBytes: 100 });
    render(<FileField value={null} onChange={vi.fn()} variant="avatar" uploadFn={fn} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [fakeFile("me.png", "image/png", 100)] } });
    expect(screen.getByRole("button", { name: "Apply" })).toBeTruthy();
    expect(fn).not.toHaveBeenCalled();
  });

  it("disables the browse button and existing remove controls when disabled", () => {
    const value = { fileId: "f1", name: "report.pdf", contentType: "application/pdf", sizeBytes: 1024 };
    render(<FileField value={value} onChange={vi.fn()} disabled />);
    expect((screen.getByRole("button", { name: "Remove report.pdf" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
