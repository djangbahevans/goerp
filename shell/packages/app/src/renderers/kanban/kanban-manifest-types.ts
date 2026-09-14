import type { ListAction, ListFilter } from "../list/list-view-types.js";

// manifest-spec.md §9.3's Kanban View wire schema, resolved by
// kanban-renderer.tsx into KanbanBoardProps.
export interface KanbanViewDeclaration {
  name: string;
  type: "kanban";
  resource: string;
  label: string;
  icon?: string;
  permission?: string;
  group_by: string;
  group_values?: string[];
  group_label_field?: string;
  group_color_field?: string;
  card_fields: string[];
  card_component?: string;
  card_actions?: ListAction[];
  column_actions?: ListAction[];
  quick_create?: boolean;
  quick_create_fields?: string[];
  allow_drag?: boolean;
  drag_updates_field?: string;
  max_cards_per_column?: number;
  default_filters?: Record<string, unknown>;
  filters?: ListFilter[];
  actions?: ListAction[];
}
