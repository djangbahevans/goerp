import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { buildEmptyViewRegistry, type FieldDef, ViewRegistryContext } from "@goerp/sdk/schema";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import { createQueryClient } from "../query-client.js";
import { FormFieldRow } from "./form/form-fields.js";
import { renderCellContent } from "./list/column-renderers.js";
import { useModelFields } from "./record-values.js";

const RESOURCE = "sales.order";
const ROW = {
  customer_id: "c1",
  buyer: { id: "c1", display_name: "Acme" },
  client: { id: "c1", display_name: "Globex" },
  tag_ids: ["t1"],
  labels: [{ id: "t1", name: "VIP", color: "#10B981" }],
};

function Values() {
  const fields = useModelFields(RESOURCE);
  return (
    <div className="grid max-w-lg gap-6 p-6">
      <FormFieldRow
        field={{ field: "customer_id", label: "Customer", type: "relation" }}
        resource={RESOURCE}
        record={ROW}
        onChange={() => {}}
        formReadonly
      />
      <FormFieldRow
        field={{ field: "tag_ids", label: "Tags", type: "tags" }}
        resource={RESOURCE}
        record={ROW}
        onChange={() => {}}
        formReadonly
      />
      <table>
        <caption>List values</caption>
        <tbody>
          <tr>
            <td>{renderCellContent({ field: "customer_id", type: "relation" }, ROW, { fields })}</td>
          </tr>
          <tr>
            <td>{renderCellContent({ field: "tag_ids", type: "tags" }, ROW, { fields })}</td>
          </tr>
          <tr>
            <td>
              {renderCellContent(
                { field: "customer_id", type: "relation", display_field: "flat_label" },
                { ...ROW, flat_label: "Flat label" },
                { fields },
              )}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  );
}

function DeclaredReadKeys() {
  const [key, setKey] = useState("buyer");
  const [client] = useState(() => createQueryClient());
  const registry = buildEmptyViewRegistry();
  const fields: FieldDef[] = [
    { name: "customer_id", type: "many2one", read_key: key },
    { name: "tag_ids", type: "many2many", read_key: "labels" },
  ];
  registry.models.set(RESOURCE, {
    name: "order",
    label: "Order",
    label_plural: "Orders",
    fields,
    enabled_ops: [],
    shareable: false,
  });
  return (
    <QueryClientProvider client={client}>
      <PermissionContext
        value={createPermissionContextValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() })}
      >
        <ViewRegistryContext value={registry}>
          <button type="button" onClick={() => setKey("client")}>
            Refresh schema
          </button>
          <Values />
        </ViewRegistryContext>
      </PermissionContext>
    </QueryClientProvider>
  );
}

export default { title: "Renderers/DeclaredReadKeys", component: DeclaredReadKeys } satisfies Meta<
  typeof DeclaredReadKeys
>;
type Story = StoryObj<typeof DeclaredReadKeys>;

export const SchemaRefresh: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getAllByText("Acme")).toHaveLength(2);
    await expect(canvas.getAllByText("VIP")).toHaveLength(2);
    await expect(canvas.getByText("Flat label")).toBeInTheDocument();
    await userEvent.click(canvas.getByRole("button", { name: "Refresh schema" }));
    await expect(canvas.getAllByText("Globex")).toHaveLength(2);
    await expect(canvas.queryByText("Acme")).not.toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Refresh schema" })).toHaveFocus();
  },
};
