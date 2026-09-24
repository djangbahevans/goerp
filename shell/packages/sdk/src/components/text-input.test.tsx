import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Button } from "./button.js";
import { FieldWrapper } from "./field-wrapper.js";
import { IconButton } from "./icon-button.js";
import { TextArea, TextInput } from "./text-input.js";

afterEach(cleanup);

describe("TextInput", () => {
  it("calls onChange with the new string", () => {
    const onChange = vi.fn();
    render(<TextInput aria-label="Name" value="" onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Ama" } });
    expect(onChange).toHaveBeenCalledWith("Ama");
  });

  it("passes native attributes and the ref through to the <input>", () => {
    let node: HTMLInputElement | null = null;
    render(
      <TextInput
        ref={(el) => {
          node = el;
        }}
        aria-label="Email"
        type="email"
        name="email"
        autoComplete="email"
        placeholder="name@company.com"
        value=""
        onChange={() => {}}
      />,
    );
    const input = screen.getByLabelText("Email") as HTMLInputElement;
    expect(node).toBe(input);
    expect(input.type).toBe("email");
    expect(input.name).toBe("email");
    expect(input.autocomplete).toBe("email");
    expect(input.placeholder).toBe("name@company.com");
  });

  it("ignores className so callers can't restyle it", () => {
    const props = { className: "bg-danger" } as unknown as Record<string, never>;
    render(<TextInput aria-label="Name" value="" onChange={() => {}} {...props} />);
    expect(screen.getByLabelText("Name").className).not.toContain("bg-danger");
  });

  it("marks a bare input invalid with `invalid`", () => {
    render(<TextInput aria-label="Name" value="" onChange={() => {}} invalid />);
    expect(screen.getByLabelText("Name").getAttribute("aria-invalid")).toBe("true");
  });

  it("lets an explicit `invalid={false}` override the wrapper's error", () => {
    render(
      <FieldWrapper label="Name" error="Required">
        <TextInput value="" onChange={() => {}} invalid={false} />
      </FieldWrapper>,
    );
    expect(screen.getByLabelText("Name").hasAttribute("aria-invalid")).toBe(false);
  });

  it("appends its own aria-describedby to the wrapper's", () => {
    render(
      <>
        <span id="hint">Hint</span>
        <FieldWrapper label="Name" error="Required">
          <TextInput value="" onChange={() => {}} aria-describedby="hint" />
        </FieldWrapper>
      </>,
    );
    const ids = screen.getByLabelText("Name").getAttribute("aria-describedby")?.split(" ");
    expect(ids).toEqual([screen.getByRole("alert").id, "hint"]);
  });

  it("renders start and end content inside the same box as the input", () => {
    render(
      <TextInput
        aria-label="Search"
        type="search"
        value="abc"
        onChange={() => {}}
        start={<span data-testid="start">@</span>}
        end={<IconButton icon="x" label="Clear" size="sm" />}
      />,
    );
    const box = screen.getByLabelText("Search").parentElement;
    expect(box?.contains(screen.getByTestId("start"))).toBe(true);
    expect(box?.contains(screen.getByRole("button", { name: "Clear" }))).toBe(true);
  });

  it("keeps the same <input> and its focus when a slot appears", () => {
    function Harness() {
      const [value, setValue] = useState("");
      return (
        <TextInput
          aria-label="Country"
          value={value}
          onChange={setValue}
          end={value ? <IconButton icon="x" label="Clear" size="sm" onClick={() => setValue("")} /> : undefined}
        />
      );
    }
    render(<Harness />);
    const input = screen.getByLabelText("Country");
    input.focus();
    fireEvent.change(input, { target: { value: "Ghana" } });
    expect(screen.getByLabelText("Country")).toBe(input);
    expect(document.activeElement).toBe(input);
  });

  it("matches Button's height at each size", () => {
    for (const [size, height] of [
      ["md", "h-9"],
      ["sm", "h-7"],
    ] as const) {
      const { unmount } = render(
        <>
          <TextInput aria-label="Filter" size={size} value="" onChange={() => {}} />
          <Button size={size}>Apply</Button>
        </>,
      );
      expect(screen.getByLabelText("Filter").parentElement?.className).toContain(height);
      expect(screen.getByRole("button", { name: "Apply" }).className).toContain(height);
      unmount();
    }
  });

  it("renders in sans at --text-base with the control border", () => {
    render(<TextInput aria-label="Name" value="" onChange={() => {}} />);
    const box = screen.getByLabelText("Name").parentElement?.className ?? "";
    expect(box).toContain("font-sans");
    expect(box).toContain("text-base");
    expect(box).toContain("border-border-control");
    expect(box).toContain("hover:border-text-secondary");
  });
});

describe("TextArea", () => {
  it("calls onChange with the new string and defaults to three rows", () => {
    const onChange = vi.fn();
    render(<TextArea aria-label="Notes" value="" onChange={onChange} />);
    const textarea = screen.getByLabelText("Notes") as HTMLTextAreaElement;
    expect(textarea.rows).toBe(3);
    fireEvent.change(textarea, { target: { value: "line 1\nline 2" } });
    expect(onChange).toHaveBeenCalledWith("line 1\nline 2");
  });

  it("reads the FieldWrapper's wiring", () => {
    render(
      <FieldWrapper label="Notes" error="Too long" required>
        <TextArea value="" onChange={() => {}} />
      </FieldWrapper>,
    );
    const textarea = screen.getByLabelText(/Notes/) as HTMLTextAreaElement;
    expect(textarea.getAttribute("aria-invalid")).toBe("true");
    expect(textarea.getAttribute("aria-describedby")).toBe(screen.getByRole("alert").id);
    expect(textarea.required).toBe(true);
  });

  it("resizes vertically by default and not at all with resize=none", () => {
    const { rerender } = render(<TextArea aria-label="Notes" value="" onChange={() => {}} />);
    expect(screen.getByLabelText("Notes").className).toContain("resize-y");
    rerender(<TextArea aria-label="Notes" value="" onChange={() => {}} resize="none" />);
    expect(screen.getByLabelText("Notes").className).toContain("resize-none");
  });
});

describe("TextInput type=number", () => {
  it("keeps typed text that parses to the current value", () => {
    function Harness() {
      const [amount, setAmount] = useState<number | undefined>(undefined);
      return (
        <TextInput
          aria-label="Amount"
          type="number"
          value={amount === undefined ? "" : String(amount)}
          onChange={(next) => setAmount(next === "" ? undefined : Number(next))}
        />
      );
    }
    render(<Harness />);
    const input = screen.getByLabelText("Amount") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "1.0" } });
    expect(input.value).toBe("1.0");
    fireEvent.change(input, { target: { value: "1.05" } });
    expect(input.value).toBe("1.05");
  });

  it("shows a new value set from outside", () => {
    const { rerender } = render(<TextInput aria-label="Amount" type="number" value="1" onChange={() => {}} />);
    rerender(<TextInput aria-label="Amount" type="number" value="7" onChange={() => {}} />);
    expect((screen.getByLabelText("Amount") as HTMLInputElement).value).toBe("7");
  });
});

describe("TextInput combobox", () => {
  it("marks a required combobox with aria-required, not native required", () => {
    render(
      <FieldWrapper label="Country" required>
        <TextInput role="combobox" aria-expanded={false} value="" onChange={() => {}} />
      </FieldWrapper>,
    );
    const input = screen.getByRole("combobox") as HTMLInputElement;
    expect(input.required).toBe(false);
    expect(input.getAttribute("aria-required")).toBe("true");
  });

  it("focuses the input when a decorative start slot is pressed", () => {
    render(
      <TextInput aria-label="Country" value="Ghana" onChange={() => {}} start={<span data-testid="flag">GH</span>} />,
    );
    fireEvent.mouseDown(screen.getByTestId("flag"));
    expect(document.activeElement).toBe(screen.getByLabelText("Country"));
  });
});
