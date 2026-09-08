import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CodeField } from "./code-field.js";

afterEach(cleanup);

// Real typing can't be simulated reliably in jsdom (CodeMirror's own input
// path diffs live contentEditable DOM mutations, which jsdom's synthetic
// events don't reproduce) — paste exercises the same onChange plumbing
// through a path that only needs a real clipboardData.getData(), the same
// tradeoff rich-text-field.test.tsx makes by driving changes through
// toolbar clicks instead of keystrokes.
function paste(target: Element, text: string): void {
  fireEvent.paste(target, { clipboardData: { getData: (type: string) => (type === "text/plain" ? text : "") } });
}

describe("CodeField", () => {
  it("renders the label and initial value", () => {
    render(<CodeField label="Handler" value="print(1)" onChange={() => {}} />);
    expect(screen.getByLabelText("Handler")).toBeTruthy();
    expect(screen.getByText("print(1)")).toBeTruthy();
  });

  it("renders as an accessible multiline textbox", () => {
    render(<CodeField label="Handler" value="" onChange={() => {}} />);
    const field = screen.getByRole("textbox", { name: "Handler" });
    expect(field.getAttribute("aria-multiline")).toBe("true");
  });

  it("calls onChange with the updated text on paste", () => {
    const onChange = vi.fn();
    render(<CodeField label="Handler" value="" onChange={onChange} />);
    paste(screen.getByRole("textbox"), "print(2)");
    expect(onChange).toHaveBeenCalledWith("print(2)");
  });

  it("does not call onChange on mount", () => {
    const onChange = vi.fn();
    render(<CodeField label="Handler" value="print(1)" onChange={onChange} />);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not call onChange when only the disabled prop toggles", () => {
    const onChange = vi.fn();
    const { rerender } = render(<CodeField label="Handler" value="print(1)" onChange={onChange} />);
    rerender(<CodeField label="Handler" value="print(1)" onChange={onChange} disabled />);
    rerender(<CodeField label="Handler" value="print(1)" onChange={onChange} disabled={false} />);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not call onChange when value changes externally", () => {
    const onChange = vi.fn();
    const { rerender } = render(<CodeField label="Handler" value="print(1)" onChange={onChange} />);
    rerender(<CodeField label="Handler" value="print(2)" onChange={onChange} />);
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("print(2)")).toBeTruthy();
  });

  it("renders an error message and marks the editor invalid", () => {
    render(<CodeField label="Handler" value="" onChange={() => {}} error="Handler code is required" />);
    expect(screen.getByRole("alert").textContent).toBe("Handler code is required");
    expect(screen.getByRole("textbox").getAttribute("aria-invalid")).toBe("true");
  });

  it("disables editing and ignores paste when disabled", () => {
    const onChange = vi.fn();
    render(<CodeField label="Handler" value="print(1)" onChange={onChange} disabled />);
    const field = screen.getByRole("textbox");
    expect(field.getAttribute("contenteditable")).toBe("false");
    expect(field.getAttribute("aria-readonly")).toBe("true");
    paste(field, "print(2)");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("is already disabled and invalid on the very first render, not just after a subsequent effect", () => {
    render(
      <CodeField label="Handler" value="print(1)" onChange={() => {}} disabled error="Handler code is required" />,
    );
    const field = screen.getByRole("textbox");
    expect(field.getAttribute("contenteditable")).toBe("false");
    expect(field.getAttribute("aria-invalid")).toBe("true");
    expect(field.getAttribute("aria-labelledby")).toBeTruthy();
  });

  it("dims the wrapper when disabled", () => {
    // classList, not className.includes(...): the wrapper's static classes
    // already contain the substring "opacity-50" inside the inert
    // has-[:disabled]:opacity-50 utility (never matched, since nothing
    // inside is a real :disabled element) — classList.contains only matches
    // the plain "opacity-50" token this test is actually checking for.
    const { container, rerender } = render(<CodeField label="Handler" value="" onChange={() => {}} />);
    const wrapper = container.querySelector(".cm-editor")?.parentElement?.parentElement;
    expect(wrapper?.classList.contains("opacity-50")).toBe(false);
    rerender(<CodeField label="Handler" value="" onChange={() => {}} disabled />);
    expect(wrapper?.classList.contains("opacity-50")).toBe(true);
    expect(wrapper?.classList.contains("cursor-not-allowed")).toBe(true);
  });

  it("applies rows as an approximate min-height on the editor", () => {
    render(<CodeField label="Handler" value="" onChange={() => {}} rows={5} />);
    const host = screen.getByRole("textbox").closest(".cm-editor")?.parentElement;
    expect(host?.getAttribute("style")).toContain("min-height: 7.5em");
  });

  it("highlights syntax once the language prop resolves to a real language", async () => {
    render(
      <CodeField label="Handler" value="function run() { return 1; }" onChange={() => {}} language="javascript" />,
    );
    const field = screen.getByRole("textbox");
    await waitFor(() => expect(field.querySelector("span")).not.toBeNull());
  });

  it("does not trap Tab, but Escape re-arms indent-with-Tab for the next Tab press", () => {
    render(<CodeField label="Handler" value="a\nb" onChange={() => {}} />);
    const field = screen.getByRole("textbox");

    // Without escaping first, Tab is captured for indentation.
    const capturedTab = fireEvent.keyDown(field, { key: "Tab", keyCode: 9 });
    expect(capturedTab).toBe(false);

    // Escape (CodeMirror's own tab-focus-mode convention) suspends that
    // capture, so the very next Tab is left for the browser's default
    // focus-changing behavior instead.
    fireEvent.keyDown(field, { key: "Escape", keyCode: 27 });
    const releasedTab = fireEvent.keyDown(field, { key: "Tab", keyCode: 9 });
    expect(releasedTab).toBe(true);
  });
});
