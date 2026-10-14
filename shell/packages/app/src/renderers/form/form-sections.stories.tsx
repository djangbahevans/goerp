import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, within } from "storybook/test";
import { FormSectionRenderer } from "./form-sections.js";

const meta = {
  title: "Renderers/Form/Sections",
  component: FormSectionRenderer,
} satisfies Meta<typeof FormSectionRenderer>;
export default meta;
type Story = StoryObj<typeof meta>;

export const InlineColumnPermissions: Story = {
  args: {
    section: {
      type: "sub_list",
      field: "address_ids",
      inline_key: "addresses",
      label: "Addresses",
      columns: [
        { field: "name", label: "Name" },
        { field: "city", label: "City", permission: "contacts:contact:financials_read" },
        { field: "postcode", label: "Denied postcode", permission: "contacts:contact:private_read" },
        { field: "street", label: "Secured street", permission: "contacts:contact:financials_read" },
      ],
    },
    resource: "contacts.contact",
    module: "contacts",
    recordId: "c1",
    record: {
      addresses: [
        { id: "a1", name: "Head office", city: "Accra", postcode: "Restricted code", street: "Secret street" },
      ],
    },
    onChange: () => {},
    formReadonly: true,
  },
  decorators: [
    (Story) => {
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
      });
      client.setQueryData(["form-one2many-target", "contacts.contact", "address_ids"], {
        relatedModel: "contacts.address",
        inverseField: "contact_id",
      });
      const permissions = createPermissionContextValue({
        permissions: new Set(["contacts:contact:financials_read"]),
        fieldAccess: { "contacts.address": { street: { read: false, write: false } } },
        modulesEnabled: new Set(),
      });
      return (
        <QueryClientProvider client={client}>
          <PermissionContext.Provider value={permissions}>
            <Story />
          </PermissionContext.Provider>
        </QueryClientProvider>
      );
    },
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByText("Head office")).toBeInTheDocument();
    await expect(canvas.getByRole("columnheader", { name: "City" })).toBeInTheDocument();
    await expect(canvas.getByText("Accra")).toBeInTheDocument();
    await expect(canvas.queryByRole("columnheader", { name: "Denied postcode" })).not.toBeInTheDocument();
    await expect(canvas.queryByText("Restricted code")).not.toBeInTheDocument();
    await expect(canvas.queryByRole("columnheader", { name: "Secured street" })).not.toBeInTheDocument();
    await expect(canvas.queryByText("Secret street")).not.toBeInTheDocument();
  },
};
