import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { RelationField } from "./relation-field.js";

afterEach(cleanup);

describe("RelationField", () => {
  it("renders the linked record as an anchor when href is given", () => {
    render(<RelationField label="Customer" value={{ id: "1", display: "Acme Corp" }} href="/contacts/1" />);
    const link = screen.getByRole("link", { name: "Acme Corp" }) as HTMLAnchorElement;
    expect(link.getAttribute("href")).toBe("/contacts/1");
  });

  it("renders the display text without a link when href is omitted", () => {
    render(<RelationField value={{ id: "1", display: "Acme Corp" }} />);
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByText("Acme Corp")).toBeTruthy();
  });

  it("renders emptyText when value is undefined", () => {
    render(<RelationField emptyText="none" />);
    expect(screen.getByText("none")).toBeTruthy();
  });
});
