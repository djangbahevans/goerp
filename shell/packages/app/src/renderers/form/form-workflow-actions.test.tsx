import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { ModelDef } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WorkflowActions } from "./form-workflow-actions.js";

const { useActionMock, resolveModelMock } = vi.hoisted(() => ({
  useActionMock: vi.fn(),
  resolveModelMock: vi.fn(),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useAction: useActionMock };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, modelRegistry: { resolve: resolveModelMock } };
});

beforeEach(() => {
  useActionMock.mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false, error: null });
});

afterEach(() => {
  cleanup();
  useActionMock.mockReset();
  resolveModelMock.mockReset();
});

function permissionWrapper(permissions: string[]) {
  const value = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
  };
}

const fullAccess = permissionWrapper(["sales:order:confirm"]);

function orderModel(overrides: Partial<ModelDef["fields"][number]> = {}): ModelDef {
  return {
    name: "order",
    label: "Order",
    label_plural: "Orders",
    enabled_ops: [],
    shareable: false,
    fields: [
      {
        name: "state",
        type: "selection",
        workflow: {
          states: ["draft", "confirmed", "cancelled"],
          transitions: [
            { from: "draft", to: "confirmed", action_name: "confirm", permission: "sales:order:confirm" },
            { from: "confirmed", to: "cancelled", action_name: "cancel" },
          ],
        },
        ...overrides,
      },
    ],
  };
}

async function renderWorkflowActions(
  Wrapper: ({ children }: { children: ReactNode }) => ReactNode,
  props: { recordId: string | undefined; record: Record<string, unknown> },
) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const result = render(
    <QueryClientProvider client={client}>
      <Wrapper>
        <WorkflowActions resource="sales.order" module="sales" {...props} />
      </Wrapper>
    </QueryClientProvider>,
  );
  await vi.waitFor(() => expect(resolveModelMock).toHaveBeenCalled());
  return result;
}

describe("WorkflowActions", () => {
  it("renders nothing on a create form (no recordId yet)", async () => {
    resolveModelMock.mockResolvedValue(orderModel());
    await renderWorkflowActions(fullAccess, { recordId: undefined, record: { state: "draft" } });
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("renders nothing when the model declares no .Workflow() field", async () => {
    const model = orderModel();
    model.fields[0] = { name: "state", type: "selection" };
    resolveModelMock.mockResolvedValue(model);
    await renderWorkflowActions(fullAccess, { recordId: "01j", record: { state: "draft" } });
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("renders nothing when no transition is legal from the record's current state", async () => {
    resolveModelMock.mockResolvedValue(orderModel());
    await renderWorkflowActions(fullAccess, { recordId: "01j", record: { state: "cancelled" } });
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("renders one button per transition legal from the current state, title-cased from action_name", async () => {
    resolveModelMock.mockResolvedValue(orderModel());
    await renderWorkflowActions(fullAccess, { recordId: "01j", record: { state: "draft" } });
    expect(await screen.findByText("Confirm")).toBeTruthy();
    expect(screen.queryByText("Cancel")).toBeNull(); // not legal from "draft"
  });

  it("hides a transition button when the caller lacks its .Requires() permission", async () => {
    resolveModelMock.mockResolvedValue(orderModel());
    await renderWorkflowActions(permissionWrapper([]), { recordId: "01j", record: { state: "draft" } });
    await vi.waitFor(() => expect(resolveModelMock).toHaveBeenCalled());
    expect(screen.queryByText("Confirm")).toBeNull();
  });

  it("dispatches the transition's action route with the record id on click", async () => {
    const mutate = vi.fn();
    useActionMock.mockReturnValue({ mutate, isPending: false, isError: false, error: null });
    resolveModelMock.mockResolvedValue(orderModel());
    await renderWorkflowActions(fullAccess, { recordId: "01j", record: { state: "draft" } });

    const button = await screen.findByText("Confirm");
    fireEvent.click(button);

    expect(useActionMock).toHaveBeenCalledWith(
      "sales.confirm",
      expect.objectContaining({ invalidates: expect.any(Array) }),
    );
    expect(mutate).toHaveBeenCalledWith("01j");
  });
});
