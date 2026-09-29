import { AuthContext, type AuthContextValue } from "@goerp/sdk/auth";
import type { RecordReader, UseRecordReadersResult } from "@goerp/sdk/react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RecordReaderPicker, type RecordReaderPickerProps } from "./record-reader-picker.js";

const { useRecordReadersMock } = vi.hoisted(() => ({ useRecordReadersMock: vi.fn() }));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useRecordReaders: useRecordReadersMock };
});

const ama: RecordReader = { id: "u1", name: "Ama Owusu", email: "ama@acme.example", avatarUrl: null };
const kwame: RecordReader = { id: "u2", name: "Kwame Mensah", email: "kwame@acme.example", avatarUrl: null };
const nameless: RecordReader = { id: "u3", name: null, email: "ops@acme.example", avatarUrl: null };

function readers(overrides: Partial<UseRecordReadersResult> = {}): UseRecordReadersResult {
  return { readers: [ama, kwame, nameless], isLoading: false, isError: false, error: null, ...overrides };
}

const auth = { user: { id: "u1" } } as unknown as AuthContextValue;

function renderPicker(props: Partial<RecordReaderPickerProps> = {}) {
  const onChange = vi.fn();
  render(
    <AuthContext.Provider value={auth}>
      <RecordReaderPicker model="sales.order" recordId="o1" value={null} onChange={onChange} {...props} />
    </AuthContext.Provider>,
  );
  return { onChange, input: screen.getByRole("combobox") as HTMLInputElement };
}

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  useRecordReadersMock.mockReturnValue(readers());
});
afterEach(() => {
  cleanup();
  useRecordReadersMock.mockReset();
});

describe("RecordReaderPicker", () => {
  it("searches only while open", () => {
    const { input } = renderPicker({ excludeSelf: true });
    expect(useRecordReadersMock).toHaveBeenLastCalledWith("sales.order", "o1", null, { excludeSelf: true });
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "kw" } });
    expect(useRecordReadersMock).toHaveBeenLastCalledWith("sales.order", "o1", "kw", { excludeSelf: true });
  });

  it("names each option by name and email, marks the viewer, and falls back to email", () => {
    const { input } = renderPicker();
    fireEvent.focus(input);
    expect(screen.getByRole("option", { name: "Ama Owusu (you), ama@acme.example" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "Kwame Mensah, kwame@acme.example" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "ops@acme.example" })).toBeTruthy();
  });

  it("picks a reader", () => {
    const { input, onChange } = renderPicker();
    fireEvent.focus(input);
    fireEvent.click(screen.getByRole("option", { name: "Kwame Mensah, kwame@acme.example" }));
    expect(onChange).toHaveBeenCalledWith(kwame);
  });

  it("shows the chosen person's name, or email when nameless", () => {
    renderPicker({ value: kwame });
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("Kwame Mensah");
    cleanup();
    renderPicker({ value: nameless });
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("ops@acme.example");
  });

  it("offers a clear button unless clearable is false", () => {
    const { onChange } = renderPicker({ value: kwame });
    fireEvent.click(screen.getByRole("button", { name: "Clear Kwame Mensah" }));
    expect(onChange).toHaveBeenCalledWith(null);
    cleanup();
    renderPicker({ value: kwame, clearable: false });
    expect(screen.queryByRole("button", { name: "Clear Kwame Mensah" })).toBeNull();
  });

  it("explains an empty result", () => {
    useRecordReadersMock.mockReturnValue(readers({ readers: [] }));
    const { input } = renderPicker();
    fireEvent.focus(input);
    expect(screen.getByRole("listbox").textContent).toBe("No one found who can see this record.");
  });

  it("shows a failed search", () => {
    useRecordReadersMock.mockReturnValue(readers({ readers: [], isError: true }));
    const { input } = renderPicker();
    fireEvent.focus(input);
    expect(screen.getByRole("listbox").textContent).toBe("Couldn't load people.");
  });

  it("keeps the previous readers while the next search loads", () => {
    useRecordReadersMock.mockReturnValue(readers({ readers: [kwame], isLoading: true }));
    const { input } = renderPicker();
    fireEvent.focus(input);
    expect(screen.getByRole("option").textContent).toContain("Kwame Mensah");
    expect(screen.getByRole("listbox").getAttribute("aria-busy")).toBe("true");
  });
});
