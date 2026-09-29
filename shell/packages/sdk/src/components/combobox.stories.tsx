import type { Meta, StoryObj } from "@storybook/react-vite";
import { type ReactNode, useState } from "react";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { Combobox, type ComboboxStatus } from "./combobox.js";
import { FieldWrapper } from "./field-wrapper.js";
import { IconButton } from "./icon-button.js";

const FRUITS = [
  "Apple",
  "Apricot",
  "Banana",
  "Blackberry",
  "Blueberry",
  "Cherry",
  "Clementine",
  "Date",
  "Dragon fruit",
  "Elderberry",
  "Fig",
  "Grape",
  "Guava",
  "Kiwi",
  "Lemon",
  "Lime",
  "Mango",
  "Nectarine",
  "Orange",
  "Papaya",
  "Peach",
  "Pear",
  "Plum",
  "Raspberry",
  "Strawberry",
];

interface DemoProps {
  status?: ComboboxStatus;
  options?: string[];
  initial?: string;
  multiple?: boolean;
  disabled?: boolean;
}

function Demo({ status = "ready", options, initial, multiple = false, disabled }: DemoProps): ReactNode {
  const [query, setQuery] = useState("");
  const [value, setValue] = useState<string | undefined>(initial);
  const [values, setValues] = useState<string[]>([]);
  const all = options ?? FRUITS;
  const shown = all.filter((f) => f.toLowerCase().includes(query.toLowerCase()) && !values.includes(f));
  return (
    <FieldWrapper label="Fruit">
      <Combobox<string>
        query={query}
        onQueryChange={setQuery}
        options={shown}
        getOptionKey={(f) => f}
        getOptionLabel={(f) => f}
        onSelect={(f) => (multiple ? setValues([...values, f]) : setValue(f))}
        status={status}
        selectedLabel={multiple ? undefined : value}
        onClear={multiple ? undefined : () => setValue(undefined)}
        closeOnSelect={!multiple}
        keepQueryOnDismiss={multiple}
        above={
          multiple && values.length > 0 ? (
            <span className="flex flex-wrap gap-1">
              {values.map((v) => (
                <span
                  key={v}
                  className="inline-flex items-center gap-1 rounded-control bg-bg-subtle px-2 py-1 text-sm text-text"
                >
                  {v}
                  <IconButton
                    icon="x"
                    size="sm"
                    label={`Remove ${v}`}
                    onClick={() => setValues(values.filter((x) => x !== v))}
                  />
                </span>
              ))}
            </span>
          ) : undefined
        }
        onKeyDown={(event) => {
          if (multiple && event.key === "Backspace" && query === "" && values.length > 0) {
            event.preventDefault();
            setValues(values.slice(0, -1));
          }
        }}
        disabled={disabled}
        placeholder="Search fruit…"
      />
    </FieldWrapper>
  );
}

const meta: Meta<typeof Demo> = {
  title: "Form Fields/Combobox",
  component: Demo,
  decorators: [
    (Story) => (
      <div className="max-w-xs">
        <Story />
      </div>
    ),
  ],
};

export default meta;

type Story = StoryObj<typeof Demo>;

const body = within(document.body);

export const Open: Story = {
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("combobox", { name: "Fruit" }));
    await waitFor(() => expect(body.getByRole("option", { name: "Apple" })).toBeInTheDocument());
  },
};

export const KeyboardPaging: Story = {
  name: "keyboard: End, PageUp, filter",
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole("combobox", { name: "Fruit" });
    await userEvent.click(input);
    await userEvent.keyboard("{End}{PageUp}");
    const active = () => document.getElementById(input.getAttribute("aria-activedescendant") ?? "")?.textContent;
    await waitFor(() => expect(active()).toBe(FRUITS[FRUITS.length - 11]));
    await userEvent.type(input, "berr");
    await waitFor(() => expect(active()).toBe("Blackberry"));
  },
};

export const WithValue: Story = { args: { initial: "Mango" } };

export const MultiSelect: Story = {
  name: "multi-select: stays open, pills above",
  args: { multiple: true },
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole("combobox", { name: "Fruit" });
    await userEvent.click(input);
    await userEvent.click(await body.findByRole("option", { name: "Apple" }));
    await userEvent.click(body.getByRole("option", { name: "Banana" }));
    await waitFor(() => expect(input).toHaveFocus());
    expect(input).toHaveAttribute("aria-expanded", "true");
    // Each pill removes its own pick; Backspace on an empty query removes the last.
    await userEvent.click(within(canvasElement).getByRole("button", { name: "Remove Apple" }));
    await waitFor(() => expect(within(canvasElement).queryByText("Apple")).not.toBeInTheDocument());
    // The pill row pushes the input down; the panel follows it instead of covering it.
    await waitFor(() =>
      expect(body.getByRole("listbox").getBoundingClientRect().top).toBeGreaterThanOrEqual(
        input.getBoundingClientRect().bottom,
      ),
    );
  },
};

export const NoResults: Story = {
  play: async ({ canvasElement }) => {
    await userEvent.type(within(canvasElement).getByRole("combobox", { name: "Fruit" }), "zzz");
    await waitFor(() => expect(body.getByRole("status")).toHaveTextContent('No results for "zzz"'));
  },
};

export const Loading: Story = {
  args: { status: "loading", options: [] },
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("combobox", { name: "Fruit" }));
  },
};

export const LoadingWithResults: Story = {
  name: "loading, previous results kept",
  args: { status: "loading", options: ["Apple", "Apricot"] },
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("combobox", { name: "Fruit" }));
  },
};

export const Failed: Story = {
  args: { status: "error", options: [] },
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("combobox", { name: "Fruit" }));
  },
};

export const Disabled: Story = { args: { disabled: true, initial: "Mango" } };

export const NearViewportBottom: Story = {
  name: "near the viewport bottom: panel flips above",
  decorators: [
    (Story) => (
      <div className="flex h-[calc(100vh-4rem)] max-w-xs flex-col justify-end">
        <Story />
      </div>
    ),
  ],
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole("combobox", { name: "Fruit" });
    await userEvent.click(input);
    await waitFor(() =>
      expect(body.getByRole("listbox").getBoundingClientRect().bottom).toBeLessThanOrEqual(
        input.getBoundingClientRect().top,
      ),
    );
  },
};
