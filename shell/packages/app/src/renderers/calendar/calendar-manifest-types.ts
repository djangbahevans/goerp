import type { ListFilter } from "../list/list-view-types.js";
import type { CalendarViewMode } from "./calendar-view-types.js";

// manifest-spec.md §9.4's Calendar View wire schema, resolved by
// calendar-renderer.tsx into CalendarViewProps.
export interface CalendarViewDeclaration {
  name: string;
  type: "calendar";
  resource: string;
  label: string;
  icon?: string;
  permission?: string;
  date_field: string;
  end_date_field?: string;
  title_field: string;
  color_field?: string;
  color_map?: Record<string, string>;
  default_view?: CalendarViewMode;
  allowed_views?: CalendarViewMode[];
  filters?: ListFilter[];
  default_filters?: Record<string, unknown>;
  on_click?: string;
  on_date_click?: string;
  quick_create?: boolean;
}
