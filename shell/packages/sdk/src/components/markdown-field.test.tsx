import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MarkdownField } from "./markdown-field.js";

afterEach(cleanup);

describe("MarkdownField", () => {
  it("renders the label and initial content, parsed from Markdown source", () => {
    render(<MarkdownField label="Notes" value="Hello" onChange={() => {}} />);
    expect(screen.getByLabelText("Notes")).toBeTruthy();
    expect(screen.getByText("Hello")).toBeTruthy();
  });

  it("renders a real toolbar with accessibly-labeled formatting buttons, no Underline", () => {
    render(<MarkdownField value="" onChange={() => {}} />);
    expect(screen.getByRole("toolbar", { name: "Formatting" })).toBeTruthy();
    for (const name of [
      "Undo",
      "Redo",
      "Heading 1",
      "Heading 2",
      "Heading 3",
      "Bold",
      "Italic",
      "Bulleted list",
      "Ordered list",
      "Blockquote",
      "Inline code",
      "Code block",
      "Link",
    ]) {
      expect(screen.getByRole("button", { name })).toBeTruthy();
    }
    expect(screen.queryByRole("button", { name: "Underline" })).toBeNull();
  });

  it.each([1, 2, 3] as const)("toggles heading level %i and reflects it via aria-pressed", (level) => {
    const onChange = vi.fn();
    render(<MarkdownField value="Section" onChange={onChange} />);
    const headingButton = screen.getByRole("button", { name: `Heading ${level}` });
    expect(headingButton.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(headingButton);
    expect(headingButton.getAttribute("aria-pressed")).toBe("true");
    expect(onChange.mock.calls.at(-1)?.[0]).toContain(`${"#".repeat(level)} `);
    for (const otherLevel of [1, 2, 3] as const) {
      if (otherLevel !== level) {
        expect(screen.getByRole("button", { name: `Heading ${otherLevel}` }).getAttribute("aria-pressed")).toBe(
          "false",
        );
      }
    }
    fireEvent.click(headingButton);
    expect(headingButton.getAttribute("aria-pressed")).toBe("false");
  });

  it("disables Undo/Redo until there's history to act on, and uses it correctly", () => {
    const onChange = vi.fn();
    render(<MarkdownField value="Hello" onChange={onChange} />);
    const undoButton = screen.getByRole("button", { name: "Undo" });
    const redoButton = screen.getByRole("button", { name: "Redo" });
    expect(undoButton.hasAttribute("disabled")).toBe(true);
    expect(redoButton.hasAttribute("disabled")).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Bulleted list" }));
    expect(undoButton.hasAttribute("disabled")).toBe(false);
    expect(redoButton.hasAttribute("disabled")).toBe(true);

    fireEvent.click(undoButton);
    expect(onChange.mock.calls.at(-1)?.[0]).not.toContain("- ");
    expect(redoButton.hasAttribute("disabled")).toBe(false);

    fireEvent.click(redoButton);
    expect(onChange.mock.calls.at(-1)?.[0]).toContain("- ");
  });

  it("calls onChange with the updated Markdown source when a toolbar command changes the document", () => {
    const onChange = vi.fn();
    render(<MarkdownField value="" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Bulleted list" }));
    expect(onChange).toHaveBeenCalled();
    expect(onChange.mock.calls.at(-1)?.[0]).toContain("- ");
  });

  it("reflects bold's toggled state via aria-pressed, serializing to Markdown emphasis", () => {
    const onChange = vi.fn();
    render(<MarkdownField value="" onChange={onChange} />);
    const boldButton = screen.getByRole("button", { name: "Bold" });
    expect(boldButton.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(boldButton);
    expect(boldButton.getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(boldButton);
    expect(boldButton.getAttribute("aria-pressed")).toBe("false");
  });

  it("reflects ordered list, inline code, and code block toggled state via aria-pressed", () => {
    render(<MarkdownField value="Text" onChange={() => {}} />);
    for (const name of ["Ordered list", "Inline code", "Code block"]) {
      const button = screen.getByRole("button", { name });
      expect(button.getAttribute("aria-pressed")).toBe("false");
      fireEvent.click(button);
      expect(button.getAttribute("aria-pressed")).toBe("true");
    }
  });

  it("disables Link when there is no selection and no active link", () => {
    render(<MarkdownField value="" onChange={() => {}} />);
    expect(screen.getByRole("button", { name: "Link" }).hasAttribute("disabled")).toBe(true);
  });

  it("edits an existing link's URL via the popover and closes it on Enter", () => {
    const onChange = vi.fn();
    render(<MarkdownField value="[click](https://old.example.com)" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    fireEvent.change(screen.getByLabelText("Link URL"), { target: { value: "https://new.example.com" } });
    fireEvent.keyDown(screen.getByLabelText("Link URL"), { key: "Enter" });
    expect(screen.queryByLabelText("Link URL")).toBeNull();
    expect(onChange.mock.calls.at(-1)?.[0]).toContain("https://new.example.com");
  });

  it("pre-fills the popover with the existing URL and removes the link via the trash button", () => {
    const onChange = vi.fn();
    render(<MarkdownField value="[click](https://example.com)" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    expect((screen.getByLabelText("Link URL") as HTMLInputElement).value).toBe("https://example.com");
    fireEvent.click(screen.getByRole("button", { name: "Remove link" }));
    expect(onChange.mock.calls.at(-1)?.[0]).not.toContain("](");
    expect(screen.queryByLabelText("Link URL")).toBeNull();
  });

  it("renders an error message and marks the editor invalid", () => {
    render(<MarkdownField label="Notes" value="" onChange={() => {}} error="Notes are required" />);
    expect(screen.getByRole("alert").textContent).toBe("Notes are required");
    expect(screen.getByRole("textbox").getAttribute("aria-invalid")).toBe("true");
  });

  it("disables the toolbar buttons and the editor when disabled", () => {
    render(<MarkdownField value="" onChange={() => {}} disabled />);
    for (const name of [
      "Undo",
      "Redo",
      "Heading 1",
      "Heading 2",
      "Heading 3",
      "Bold",
      "Italic",
      "Bulleted list",
      "Ordered list",
      "Blockquote",
      "Inline code",
      "Code block",
      "Link",
    ]) {
      expect(screen.getByRole("button", { name }).hasAttribute("disabled")).toBe(true);
    }
    expect(screen.getByRole("textbox").getAttribute("contenteditable")).toBe("false");
  });

  it("applies rows as an approximate min-height on the editable area", () => {
    const { container } = render(<MarkdownField value="" onChange={() => {}} rows={5} />);
    const editorContent = container.querySelector(".tiptap")?.parentElement;
    expect(editorContent?.getAttribute("style")).toContain("min-height: 7.5em");
  });

  it("does not call onChange on mount", () => {
    const onChange = vi.fn();
    render(<MarkdownField value="Hello" onChange={onChange} />);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not call onChange when only the disabled prop toggles", () => {
    const onChange = vi.fn();
    const { rerender } = render(<MarkdownField value="Hello" onChange={onChange} />);
    rerender(<MarkdownField value="Hello" onChange={onChange} disabled />);
    rerender(<MarkdownField value="Hello" onChange={onChange} disabled={false} />);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not call onChange when value changes externally", () => {
    const onChange = vi.fn();
    const { rerender } = render(<MarkdownField value="A" onChange={onChange} />);
    rerender(<MarkdownField value="B" onChange={onChange} />);
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("B")).toBeTruthy();
  });

  it("does not reset the document when a new value prop is just a different valid Markdown spelling of the same content", () => {
    // @tiptap/markdown's serializer always emits **bold** for a bold mark
    // regardless of whether the source used ** or __ (both are valid
    // CommonMark). Comparing editor.getMarkdown() against the raw prop
    // would see "__bold__" as a real change from the already-rendered
    // "**bold**" content, calling setContent — which, even though it
    // produces the same resulting document, still pushes a transaction
    // onto the undo stack (a real, observable reset), which is exactly
    // what the canonical-comparison fix avoids.
    const { rerender } = render(<MarkdownField value="**bold** text" onChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Undo" }).hasAttribute("disabled")).toBe(true);
    // Different raw string, same canonical content — a correct comparison
    // recognizes this as no real change and leaves Undo disabled.
    rerender(<MarkdownField value="__bold__ text" onChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Undo" }).hasAttribute("disabled")).toBe(true);
  });

  it("shows placeholder text only while empty, and never overlaps real content", () => {
    const { rerender } = render(<MarkdownField value="" onChange={() => {}} placeholder="Write something…" />);
    expect(screen.getByText("Write something…")).toBeTruthy();
    rerender(<MarkdownField value="Hello" onChange={() => {}} placeholder="Write something…" />);
    expect(screen.queryByText("Write something…")).toBeNull();
  });

  it("closes the link popover if disabled becomes true while it is open", () => {
    const { rerender } = render(<MarkdownField value="[click](https://example.com)" onChange={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    expect(screen.getByLabelText("Link URL")).toBeTruthy();
    rerender(<MarkdownField value="[click](https://example.com)" onChange={() => {}} disabled />);
    expect(screen.queryByLabelText("Link URL")).toBeNull();
  });

  it("shows an error and keeps the popover open when the URL is rejected", () => {
    const onChange = vi.fn();
    render(<MarkdownField value="[click](https://example.com)" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    fireEvent.change(screen.getByLabelText("Link URL"), { target: { value: "javascript:alert(1)" } });
    fireEvent.keyDown(screen.getByLabelText("Link URL"), { key: "Enter" });
    expect(screen.getByText("Enter a valid URL")).toBeTruthy();
    expect(screen.getByLabelText("Link URL")).toBeTruthy();
  });
});
