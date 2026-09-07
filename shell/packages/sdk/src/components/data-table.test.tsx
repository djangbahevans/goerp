import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DataTable, type DataTableColumn } from "./data-table.js";

afterEach(cleanup);

interface Contact {
  id: string;
  name: string;
  email: string;
}

const columns: DataTableColumn<Contact>[] = [
  { key: "name", header: "Name", render: (c) => c.name },
  { key: "email", header: "Email", render: (c) => c.email },
];

const contacts: Contact[] = [
  { id: "1", name: "Ama Boateng", email: "ama@example.com" },
  { id: "2", name: "Kwame Mensah", email: "kwame@example.com" },
];

describe("DataTable", () => {
  it("renders headers and rows", () => {
    render(<DataTable columns={columns} data={contacts} keyExtractor={(c) => c.id} />);
    expect(screen.getByRole("columnheader", { name: "Name" })).toBeTruthy();
    expect(screen.getByText("Ama Boateng")).toBeTruthy();
    expect(screen.getByText("kwame@example.com")).toBeTruthy();
  });

  it("shows a skeleton sized to the column count while loading", () => {
    render(<DataTable columns={columns} data={contacts} keyExtractor={(c) => c.id} isLoading />);
    expect(screen.queryByText("Ama Boateng")).toBeNull();
    expect(document.querySelectorAll("[data-skeleton-cell]")).toHaveLength(columns.length * 3);
  });

  it("renders the default empty state when there is no data", () => {
    render(<DataTable columns={columns} data={[]} keyExtractor={(c) => c.id} />);
    expect(screen.getByText("No data")).toBeTruthy();
  });

  it("renders a custom empty state when provided", () => {
    render(<DataTable columns={columns} data={[]} keyExtractor={(c) => c.id} emptyState={<p>No contacts yet</p>} />);
    expect(screen.getByText("No contacts yet")).toBeTruthy();
  });

  it("calls onRowClick when a row is clicked", () => {
    const onRowClick = vi.fn();
    render(<DataTable columns={columns} data={contacts} keyExtractor={(c) => c.id} onRowClick={onRowClick} />);
    fireEvent.click(screen.getByText("Ama Boateng").closest("tr") as HTMLElement);
    expect(onRowClick).toHaveBeenCalledWith(contacts[0]);
  });

  it("calls onRowClick on Enter when a row is focused", () => {
    const onRowClick = vi.fn();
    render(<DataTable columns={columns} data={contacts} keyExtractor={(c) => c.id} onRowClick={onRowClick} />);
    const row = screen.getByText("Kwame Mensah").closest("tr") as HTMLElement;
    fireEvent.keyDown(row, { key: "Enter" });
    expect(onRowClick).toHaveBeenCalledWith(contacts[1]);
  });

  it("keeps a clickable row's native row role instead of overriding it with role=button", () => {
    const onRowClick = vi.fn();
    render(<DataTable columns={columns} data={contacts} keyExtractor={(c) => c.id} onRowClick={onRowClick} />);
    const row = screen.getByText("Ama Boateng").closest("tr") as HTMLElement;
    expect(row.getAttribute("role")).toBeNull();
    expect(row.getAttribute("tabIndex")).toBe("0");
  });

  it("does not make rows focusable or clickable when onRowClick is omitted", () => {
    render(<DataTable columns={columns} data={contacts} keyExtractor={(c) => c.id} />);
    const row = screen.getByText("Ama Boateng").closest("tr") as HTMLElement;
    expect(row.getAttribute("role")).toBeNull();
    expect(row.getAttribute("tabIndex")).toBeNull();
  });
});
