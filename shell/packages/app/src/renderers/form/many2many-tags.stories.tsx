import { createInfiniteListQueryOptions } from "@goerp/sdk/react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import { FieldInput } from "./field-renderers.js";

const savedTags = [{ id: "1", name: "VIP" }];
const options = [
  { id: "1", name: "VIP" },
  { id: "2", name: "Lead" },
];
const queryKey = createInfiniteListQueryOptions("contacts.tag", { limit: 100 }).queryKey;

function optionPage(rows: typeof options) {
  return { pages: [{ data: rows, meta: { cursor: null, has_more: false } }], pageParams: [undefined] };
}

function Many2ManyTags({ saved = true }: { saved?: boolean }) {
  const [value, setValue] = useState<unknown>(saved ? savedTags : undefined);
  const [pending, setPending] = useState<unknown>();
  const [client] = useState(() => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKey, optionPage(options));
    return queryClient;
  });

  return (
    <QueryClientProvider client={client}>
      <div className="grid max-w-lg gap-4 p-4">
        <FieldInput
          field={{
            field: "tag_ids",
            type: "tags",
            label: "Tags",
            resource: "contacts.tag",
            resource_label_field: "name",
          }}
          value={value}
          onChange={(next) => {
            setPending(next);
            if (!saved) setValue(next);
          }}
          record={{}}
        />
        <button
          type="button"
          onClick={() => client.setQueryData(queryKey, optionPage([...options, { id: "3", name: "New" }]))}
        >
          Refresh options
        </button>
        <output aria-label="Selected IDs">{JSON.stringify(pending)}</output>
      </div>
    </QueryClientProvider>
  );
}

export default { title: "Renderers/Many2ManyTags", component: Many2ManyTags } satisfies Meta<typeof Many2ManyTags>;
type Story = StoryObj<typeof Many2ManyTags>;

const exercise: Story["play"] = async ({ canvasElement, args }) => {
  const canvas = within(canvasElement);
  const input = canvas.getByRole("combobox");
  if (!args.saved) {
    await userEvent.type(input, "VIP");
    await userEvent.click(await canvas.findByRole("option", { name: "VIP" }));
  }
  await userEvent.type(input, "Lead");
  await userEvent.click(await canvas.findByRole("option", { name: "Lead" }));
  await expect(canvas.getByLabelText("Selected IDs")).toHaveTextContent('["1","2"]');

  await userEvent.click(canvas.getByRole("button", { name: "Refresh options" }));
  await userEvent.type(input, "New");
  await expect(await canvas.findByRole("option", { name: "New" })).toBeVisible();
  await expect(canvas.getByLabelText("Remove tag: VIP")).toBeVisible();
  await expect(canvas.getByLabelText("Remove tag: Lead")).toBeVisible();
  await expect(input).toHaveFocus();
  await expect(canvasElement.scrollWidth).toBeLessThanOrEqual(canvasElement.clientWidth);
};

export const SavedRecord: Story = { args: { saved: true }, play: exercise };
export const NewRecord: Story = { args: { saved: false }, play: exercise };
