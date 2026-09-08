import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RichTextField } from "./rich-text-field.js";

afterEach(cleanup);

describe("RichTextField", () => {
  it("renders the label and initial content", () => {
    render(<RichTextField label="Notes" value="<p>Hello</p>" onChange={() => {}} />);
    expect(screen.getByLabelText("Notes")).toBeTruthy();
    expect(screen.getByText("Hello")).toBeTruthy();
  });

  it("renders a real toolbar with accessibly-labeled formatting buttons", () => {
    render(<RichTextField value="" onChange={() => {}} />);
    expect(screen.getByRole("toolbar", { name: "Formatting" })).toBeTruthy();
    for (const name of [
      "Undo",
      "Redo",
      "Heading 1",
      "Heading 2",
      "Heading 3",
      "Bold",
      "Italic",
      "Underline",
      "Bulleted list",
      "Blockquote",
      "Link",
    ]) {
      expect(screen.getByRole("button", { name })).toBeTruthy();
    }
  });

  it.each([1, 2, 3] as const)("toggles heading level %i and reflects it via aria-pressed", (level) => {
    const onChange = vi.fn();
    render(<RichTextField value="<p>Section</p>" onChange={onChange} />);
    const headingButton = screen.getByRole("button", { name: `Heading ${level}` });
    expect(headingButton.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(headingButton);
    expect(headingButton.getAttribute("aria-pressed")).toBe("true");
    expect(onChange.mock.calls.at(-1)?.[0]).toContain(`<h${level}>`);
    // The other two heading levels must stay unpressed — catches a mixed-up
    // level in HEADING_ICONS/headingActive as well as the level just set.
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
    render(<RichTextField value="<p>Hello</p>" onChange={onChange} />);
    const undoButton = screen.getByRole("button", { name: "Undo" });
    const redoButton = screen.getByRole("button", { name: "Redo" });
    expect(undoButton.hasAttribute("disabled")).toBe(true);
    expect(redoButton.hasAttribute("disabled")).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Bulleted list" }));
    expect(undoButton.hasAttribute("disabled")).toBe(false);
    expect(redoButton.hasAttribute("disabled")).toBe(true);

    fireEvent.click(undoButton);
    expect(onChange.mock.calls.at(-1)?.[0]).not.toContain("<ul>");
    expect(redoButton.hasAttribute("disabled")).toBe(false);

    fireEvent.click(redoButton);
    expect(onChange.mock.calls.at(-1)?.[0]).toContain("<ul>");
  });

  it("calls onChange with the updated HTML when a toolbar command changes the document", () => {
    const onChange = vi.fn();
    render(<RichTextField value="" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Bulleted list" }));
    expect(onChange).toHaveBeenCalled();
    expect(onChange.mock.calls.at(-1)?.[0]).toContain("<ul>");
  });

  it("reflects bold's toggled state via aria-pressed", () => {
    render(<RichTextField value="" onChange={() => {}} />);
    const boldButton = screen.getByRole("button", { name: "Bold" });
    expect(boldButton.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(boldButton);
    expect(boldButton.getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(boldButton);
    expect(boldButton.getAttribute("aria-pressed")).toBe("false");
  });

  it("disables Link when there is no selection and no active link", () => {
    render(<RichTextField value="" onChange={() => {}} />);
    expect(screen.getByRole("button", { name: "Link" }).hasAttribute("disabled")).toBe(true);
  });

  it("reveals a link URL popover when Link is clicked on an active link, and hides it again on Escape", () => {
    render(<RichTextField value='<p><a href="https://example.com">click</a></p>' onChange={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    const urlInput = screen.getByLabelText("Link URL");
    expect(urlInput).toBeTruthy();
    fireEvent.keyDown(urlInput, { key: "Escape" });
    expect(screen.queryByLabelText("Link URL")).toBeNull();
  });

  it("edits an existing link's URL via the popover and closes it on Enter", () => {
    const onChange = vi.fn();
    render(<RichTextField value='<p><a href="https://old.example.com">click</a></p>' onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    fireEvent.change(screen.getByLabelText("Link URL"), { target: { value: "https://new.example.com" } });
    fireEvent.keyDown(screen.getByLabelText("Link URL"), { key: "Enter" });
    expect(screen.queryByLabelText("Link URL")).toBeNull();
    expect(onChange.mock.calls.at(-1)?.[0]).toContain('href="https://new.example.com"');
  });

  it("pre-fills the popover with the existing URL and removes the link via the trash button", () => {
    const onChange = vi.fn();
    render(<RichTextField value='<p><a href="https://example.com">click</a></p>' onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    expect((screen.getByLabelText("Link URL") as HTMLInputElement).value).toBe("https://example.com");
    fireEvent.click(screen.getByRole("button", { name: "Remove link" }));
    expect(onChange.mock.calls.at(-1)?.[0]).not.toContain("<a");
    expect(screen.queryByLabelText("Link URL")).toBeNull();
  });

  it("renders an error message and marks the editor invalid", () => {
    render(<RichTextField label="Notes" value="" onChange={() => {}} error="Notes are required" />);
    expect(screen.getByRole("alert").textContent).toBe("Notes are required");
    expect(screen.getByRole("textbox").getAttribute("aria-invalid")).toBe("true");
  });

  it("disables the toolbar buttons and the editor when disabled", () => {
    render(<RichTextField value="" onChange={() => {}} disabled />);
    for (const name of [
      "Undo",
      "Redo",
      "Heading 1",
      "Heading 2",
      "Heading 3",
      "Bold",
      "Italic",
      "Underline",
      "Bulleted list",
      "Blockquote",
      "Link",
    ]) {
      expect(screen.getByRole("button", { name }).hasAttribute("disabled")).toBe(true);
    }
    expect(screen.getByRole("textbox").getAttribute("contenteditable")).toBe("false");
  });

  it("applies rows as an approximate min-height on the editable area", () => {
    const { container } = render(<RichTextField value="" onChange={() => {}} rows={5} />);
    const editorContent = container.querySelector(".tiptap")?.parentElement;
    expect(editorContent?.getAttribute("style")).toContain("min-height: 7.5em");
  });

  it("does not call onChange on mount", () => {
    const onChange = vi.fn();
    render(<RichTextField value="<p>Hello</p>" onChange={onChange} />);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not call onChange when only the disabled prop toggles", () => {
    const onChange = vi.fn();
    const { rerender } = render(<RichTextField value="<p>Hello</p>" onChange={onChange} />);
    rerender(<RichTextField value="<p>Hello</p>" onChange={onChange} disabled />);
    rerender(<RichTextField value="<p>Hello</p>" onChange={onChange} disabled={false} />);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not call onChange when value changes externally", () => {
    const onChange = vi.fn();
    const { rerender } = render(<RichTextField value="<p>A</p>" onChange={onChange} />);
    rerender(<RichTextField value="<p>B</p>" onChange={onChange} />);
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("B")).toBeTruthy();
  });

  it("closes the link popover if disabled becomes true while it is open", () => {
    const { rerender } = render(
      <RichTextField value='<p><a href="https://example.com">click</a></p>' onChange={() => {}} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    expect(screen.getByLabelText("Link URL")).toBeTruthy();
    rerender(<RichTextField value='<p><a href="https://example.com">click</a></p>' onChange={() => {}} disabled />);
    expect(screen.queryByLabelText("Link URL")).toBeNull();
  });

  it("shows an error and keeps the popover open when the URL is rejected", () => {
    const onChange = vi.fn();
    render(<RichTextField value='<p><a href="https://example.com">click</a></p>' onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    fireEvent.change(screen.getByLabelText("Link URL"), { target: { value: "javascript:alert(1)" } });
    fireEvent.keyDown(screen.getByLabelText("Link URL"), { key: "Enter" });
    expect(screen.getByText("Enter a valid URL")).toBeTruthy();
    expect(screen.getByLabelText("Link URL")).toBeTruthy();
  });
});
