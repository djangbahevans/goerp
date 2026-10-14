import type { MetaSchema, ViewDeclaration } from "@goerp/sdk/schema";
import { ShellProvider, simulateRealtimeMessage } from "@goerp/sdk/testing";
import type { Meta, StoryObj } from "@storybook/react-vite";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { useMemo, useRef, useState } from "react";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { ViewDispatch } from "./view-dispatch.js";

type Renderer = "list" | "kanban" | "calendar" | "timeline";
const schema: MetaSchema = {
  engine_version: "0.0.0",
  schema_hash: "live-list-stories",
  modules: {
    crm: {
      name: "crm",
      version: "0.0.0",
      display_name: "CRM",
      navigation: [],
      view_extensions: [],
      view_extension_definitions: [],
      load_order: 0,
      permissions: [],
      error_codes: [],
      frontend: null,
      public_config: {},
      notification_types: [],
      routes: [
        {
          method: "GET",
          path: "/crm/people",
          permissions: [],
          model: "directory.person",
          crud_action: "list",
          engine_native: true,
          response_is_list: true,
        },
      ],
      models: {
        "directory.person": {
          name: "person",
          label: "Person",
          label_plural: "People",
          enabled_ops: ["list"],
          shareable: false,
          fields: [
            { name: "name", type: "text", is_primary: true },
            { name: "stage", type: "text" },
          ],
        },
      },
      views: [],
    },
  },
};

function declaration(type: Renderer): ViewDeclaration {
  const common = { name: "people", resource: "directory.person", label: "People", type };
  switch (type) {
    case "list":
      return { ...common, columns: [{ field: "name", label: "Name" }] };
    case "kanban":
      return { ...common, group_by: "stage", group_values: ["Active"], card_fields: ["name"], allow_drag: false };
    case "calendar":
      return { ...common, date_field: "start", title_field: "name", default_view: "agenda", allowed_views: ["agenda"] };
    case "timeline":
      return {
        ...common,
        start_field: "start",
        end_field: "end",
        label_field: "name",
        allow_drag: false,
        allow_resize: false,
      };
  }
}

function LiveList({ type }: { type: Renderer }) {
  const name = useRef("Original person");
  const [requests, setRequests] = useState(0);
  const [mounted, setMounted] = useState(true);
  const requestHandler = useMemo(
    () => async (request: Request) => {
      if (!new URL(request.url).pathname.endsWith("/crm/people")) return undefined;
      setRequests((count) => count + 1);
      const date = new Date().toISOString().slice(0, 10);
      return Response.json({
        data: [{ id: "1", name: name.current, stage: "Active", start: date, end: date }],
        meta: { has_more: false, cursor: null },
      });
    },
    [],
  );
  const router = useMemo(() => {
    const root = createRootRoute();
    const view = declaration(type);
    const route = createRoute({
      getParentRoute: () => root,
      path: "/",
      component: () => <ViewDispatch view={view} module="crm" embedded />,
    });
    return createRouter({
      routeTree: root.addChildren([route]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
  }, [type]);

  return (
    <ShellProvider module="crm" schema={schema} requestHandler={requestHandler}>
      <div className="space-y-4">
        <div className="flex gap-3">
          <button
            type="button"
            onClick={() => {
              name.current = "Updated person";
              simulateRealtimeMessage("feed:crm", {
                type: "custom",
                event: "records.changed",
                payload: { resource: "directory.person" },
              });
            }}
          >
            Publish person change
          </button>
          <button
            type="button"
            onClick={() =>
              simulateRealtimeMessage("feed:crm", {
                type: "custom",
                event: "records.changed",
                payload: { resource: "directory.company" },
              })
            }
          >
            Publish company change
          </button>
          <button type="button" onClick={() => setMounted(false)}>
            Close view
          </button>
          <output aria-label="List requests">{requests}</output>
        </div>
        {mounted && <RouterProvider router={router} />}
      </div>
    </ShellProvider>
  );
}

const meta = { title: "Shell/Renderers/Live list feeds", component: LiveList } satisfies Meta<typeof LiveList>;
export default meta;
type Story = StoryObj<typeof meta>;

const play: Story["play"] = async ({ canvasElement, args }) => {
  const canvas = within(canvasElement);
  const findPerson = (name: string) => (args.type === "timeline" ? canvas.findByTitle(name) : canvas.findByText(name));
  const queryPerson = (name: string) =>
    args.type === "timeline" ? canvas.queryByTitle(name) : canvas.queryByText(name);
  await findPerson("Original person");
  const requests = canvas.getByLabelText("List requests");
  await expect(requests).toHaveTextContent("1");
  await userEvent.click(canvas.getByRole("button", { name: "Publish company change" }));
  await expect(requests).toHaveTextContent("1");
  const publish = canvas.getByRole("button", { name: "Publish person change" });
  await userEvent.click(publish);
  await findPerson("Updated person");
  await expect(requests).toHaveTextContent("2");
  await expect(publish).toHaveFocus();
  await expect(queryPerson("Original person")).not.toBeInTheDocument();
  await userEvent.click(canvas.getByRole("button", { name: "Close view" }));
  await waitFor(() => expect(queryPerson("Updated person")).not.toBeInTheDocument());
  await userEvent.click(publish);
  await expect(requests).toHaveTextContent("2");
};

export const List: Story = { args: { type: "list" }, play };
export const Kanban: Story = { args: { type: "kanban" }, play };
export const Calendar: Story = { args: { type: "calendar" }, play };
export const Timeline: Story = { args: { type: "timeline" }, play };
