import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { PageHeader } from "./page-header.js";

afterEach(cleanup);

describe("PageHeader", () => {
  it("renders the title", () => {
    render(<PageHeader title="Contacts" />);
    expect(screen.getByRole("heading", { name: "Contacts" })).toBeTruthy();
  });

  it("renders the subtitle only when given", () => {
    render(<PageHeader title="Contacts" subtitle="4,823 contacts" />);
    expect(screen.getByText("4,823 contacts")).toBeTruthy();

    cleanup();
    render(<PageHeader title="Contacts" />);
    expect(screen.queryByText("4,823 contacts")).toBeNull();
  });

  it("renders the given actions", () => {
    render(<PageHeader title="Contacts" actions={<button type="button">New Contact</button>} />);
    expect(screen.getByRole("button", { name: "New Contact" })).toBeTruthy();
  });

  it("has no breadcrumbs prop — the shell's global header owns that trail", () => {
    // @ts-expect-error breadcrumbs isn't part of PageHeaderProps
    render(<PageHeader title="Contacts" breadcrumbs={["Home", "Contacts"]} />);
    expect(screen.queryByText("Home")).toBeNull();
  });
});
