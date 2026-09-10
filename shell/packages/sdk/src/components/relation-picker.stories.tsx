import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor, within } from "storybook/test";
import type { ResourceRegistryEntry } from "../schema/index.js";
import { RelationPicker } from "./relation-picker.js";

const ROWS = [
  { id: "1", display_name: "Acme Corp" },
  { id: "2", display_name: "Acme Industries" },
  { id: "3", display_name: "Globex Corporation" },
];

function client(rows = ROWS) {
  return { get: async <T,>() => ({ data: rows }) as T };
}

// Only listPath is read by RelationPicker — the rest of ResourceRegistryEntry
// is irrelevant here, so this stands in for the real registry's resolve().
function registry(listPath = "/contacts") {
  return { resolve: async () => ({ listPath }) as unknown as ResourceRegistryEntry };
}

const meta: Meta<typeof RelationPicker> = {
  title: "Form Fields/RelationPicker",
  component: RelationPicker,
  args: {
    resource: "contacts.contact",
    labelField: "display_name",
    onChange: () => {},
    client: client(),
    registry: registry(),
  },
};

export default meta;

type Story = StoryObj<typeof RelationPicker>;

// Opens on load so the result list is visible in the sidebar preview,
// matching action-menu.stories.tsx's "Default" convention.
export const SingleSelect: Story = {
  args: {
    value: null,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(canvas.getByRole("option", { name: "Acme Corp" })).toBeInTheDocument());
  },
};

export const SingleSelectClosedWithValue: Story = {
  args: {
    value: { id: "1", display: "Acme Corp" },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // Single-select's only way to unset a value back to null — there's no
    // pill row the way multi-select has.
    await expect(canvas.getByRole("button", { name: "Clear Acme Corp" })).toBeInTheDocument();
  },
};

export const MultiSelect: Story = {
  args: {
    value: [{ id: "1", display: "Acme Corp" }],
    multiple: true,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    // Already-selected "Acme Corp" is excluded from the result list.
    await waitFor(() => expect(canvas.getByRole("option", { name: "Acme Industries" })).toBeInTheDocument());
    await expect(canvas.queryByRole("option", { name: "Acme Corp" })).not.toBeInTheDocument();
  },
};

export const Creatable: Story = {
  args: {
    value: [],
    multiple: true,
    creatable: true,
    onCreate: (name: string) => ({ id: crypto.randomUUID(), display: name }),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("combobox");
    await userEvent.click(input);
    await userEvent.type(input, "Brand New Co");
    await waitFor(() => expect(canvas.getByRole("option", { name: 'Create "Brand New Co"' })).toBeInTheDocument());
  },
};

export const Loading: Story = {
  args: {
    value: null,
    client: { get: <T,>() => new Promise<T>(() => {}) }, // never resolves
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(canvasElement.querySelector("[data-skeleton='lines']")).toBeInTheDocument());
  },
};

export const NoResults: Story = {
  args: {
    value: null,
    client: client([]),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(canvas.getByText('No results for ""')).toBeInTheDocument());
  },
};

// manifest-spec.md §8b: an unloaded target module degrades to this state
// rather than erroring — resourceRegistry.resolve() rejects for an unknown
// resource.
export const UnregisteredResource: Story = {
  args: {
    resource: "uninstalled.module",
    value: null,
    registry: {
      resolve: (): Promise<ResourceRegistryEntry> => Promise.reject(new Error('unknown resource "uninstalled.module"')),
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox"));
    await waitFor(() => expect(canvas.getByText("Module not installed")).toBeInTheDocument());
  },
};

export const Disabled: Story = {
  args: {
    value: { id: "1", display: "Acme Corp" },
    disabled: true,
  },
};
