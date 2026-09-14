import { optionalNullable as opt } from "@goerp/sdk/schema";
import * as v from "valibot";
import { ListActionSchema, ListFilterSchema } from "../list/list-view-types.js";

// manifest-spec.md §9.3's Kanban View wire schema, resolved by
// kanban-renderer.tsx into KanbanBoardProps.
export const KanbanViewDeclarationSchema = v.looseObject({
  name: v.string(),
  type: v.literal("kanban"),
  resource: v.string(),
  label: v.string(),
  icon: opt(v.string()),
  permission: opt(v.string()),
  group_by: v.string(),
  group_values: opt(v.array(v.string())),
  group_label_field: opt(v.string()),
  group_color_field: opt(v.string()),
  card_fields: v.array(v.string()),
  card_component: opt(v.string()),
  card_actions: opt(v.array(ListActionSchema)),
  column_actions: opt(v.array(ListActionSchema)),
  quick_create: opt(v.boolean()),
  quick_create_fields: opt(v.array(v.string())),
  allow_drag: opt(v.boolean()),
  drag_updates_field: opt(v.string()),
  max_cards_per_column: opt(v.number()),
  default_filters: opt(v.record(v.string(), v.unknown())),
  filters: opt(v.array(ListFilterSchema)),
  actions: opt(v.array(ListActionSchema)),
});
export type KanbanViewDeclaration = v.InferOutput<typeof KanbanViewDeclarationSchema>;
