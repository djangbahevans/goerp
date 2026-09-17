import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GenericRenderer } from "./generic-renderer.js";

vi.mock("./view-dispatch.js", () => ({
  ViewDispatch: (props: unknown) => <div data-testid="dispatch">{JSON.stringify(props)}</div>,
}));

afterEach(cleanup);

const declaration = {
  name: "contacts_form",
  type: "form",
  resource: "contacts.contact",
  label: "Contact",
  sections: [],
};

const recordId = "0198a1b2-3c4d-7e5f-8a9b-1c2d3e4f5a6b";

describe("GenericRenderer", () => {
  it("passes a resolved recordId through to ViewDispatch", () => {
    render(
      <GenericRenderer
        resolvedView={{
          module: "contacts",
          viewName: "contacts_form",
          viewType: "form",
          declaration,
          permissions: [],
          bundleUrl: null,
          recordId,
        }}
      />,
    );
    const props = JSON.parse(screen.getByTestId("dispatch").textContent ?? "{}");
    expect(props).toMatchObject({ module: "contacts", embedded: false, recordId });
  });

  it("omits recordId entirely when the resolved view didn't match a templated route", () => {
    render(
      <GenericRenderer
        resolvedView={{
          module: "contacts",
          viewName: "contacts_list",
          viewType: "list",
          declaration: { name: "contacts_list", type: "list", resource: "contacts.contact", label: "Contacts" },
          permissions: [],
          bundleUrl: null,
        }}
      />,
    );
    const props = JSON.parse(screen.getByTestId("dispatch").textContent ?? "{}");
    expect("recordId" in props).toBe(false);
  });
});
