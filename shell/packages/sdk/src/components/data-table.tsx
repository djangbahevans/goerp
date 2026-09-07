import type { KeyboardEvent, ReactNode } from "react";
import { EmptyState } from "./empty-state.js";
import { Skeleton } from "./skeleton.js";

export interface DataTableColumn<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
}

export interface DataTableProps<T> {
  columns: DataTableColumn<T>[];
  data: T[];
  keyExtractor: (row: T) => string;
  onRowClick?: ((row: T) => void) | undefined;
  isLoading?: boolean | undefined;
  emptyState?: ReactNode | undefined;
}

export function DataTable<T>({
  columns,
  data,
  keyExtractor,
  onRowClick,
  isLoading = false,
  emptyState,
}: DataTableProps<T>): ReactNode {
  if (isLoading) {
    return <Skeleton type="table" columns={columns.length} />;
  }

  if (data.length === 0) {
    return emptyState ?? <EmptyState title="No data" />;
  }

  return (
    <table className="w-full border-collapse">
      <thead>
        <tr className="border-border border-b bg-surface">
          {columns.map((column) => (
            <th scope="col" key={column.key} className="p-3 text-left font-medium text-sm text-text-secondary">
              {column.header}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {data.map((row) => {
          const key = keyExtractor(row);
          const handleActivate = onRowClick ? () => onRowClick(row) : undefined;
          return (
            <tr
              key={key}
              onClick={handleActivate}
              tabIndex={handleActivate ? 0 : undefined}
              onKeyDown={
                handleActivate
                  ? (event: KeyboardEvent<HTMLTableRowElement>) => {
                      if (event.key !== "Enter" && event.key !== " ") return;
                      event.preventDefault();
                      handleActivate();
                    }
                  : undefined
              }
              className={`border-border border-b bg-surface ${
                handleActivate
                  ? "cursor-pointer hover:bg-surface-hover focus-visible:[outline:2px_solid_var(--color-primary)] focus-visible:-outline-offset-2"
                  : ""
              }`}
            >
              {columns.map((column) => (
                <td key={column.key} className="p-3 text-base text-text">
                  {column.render(row)}
                </td>
              ))}
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
