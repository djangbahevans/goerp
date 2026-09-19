import * as v from "valibot";

// shell-architecture.md §9's MetaSchema/ModuleSchema/RouteSchema, defined
// as valibot schemas rather than plain interfaces so schema-registry.ts can
// validate the raw GET /_meta/schema response once, at the one place every
// other registry's data ultimately comes from — a malformed field fails
// loudly right there instead of surfacing as a confusing crash somewhere
// deep in an unrelated consumer. Types are inferred from these schemas, not
// hand-duplicated, so the two can't drift.
//
// Every object below is a `looseObject`, not `object`: this schema comes
// from a live, independently-versioned backend (and third-party modules
// extending it), so an as-yet-undeclared field must survive validation
// untouched rather than being silently stripped from the parsed result —
// only the fields this SDK actually reads are ever required.
//
// Go's `omitempty` tags mean an absent field is missing from the JSON, not
// null, hence optional properties rather than `| null` here. `views`,
// `navigation`, `models`, `permissions`, and `frontend` stay loosely
// typed — goerp#575/#674's own jobs, not this module's.

export const CRUD_ACTIONS = ["get", "list", "create", "update", "delete", "pivot"] as const;
export type CRUDAction = (typeof CRUD_ACTIONS)[number];

export const RouteSchemaSchema = v.looseObject({
  method: v.string(),
  path: v.string(),
  // Genuinely nullable, not just optional: Go's Permissions []string carries
  // no `omitempty` tag, but an EnableOps-auto-generated CRUD route never
  // sets it — its zero-value nil slice marshals as JSON `null`, not `[]`.
  permissions: v.nullable(v.array(v.string())),
  model: v.optional(v.string()),
  crud_action: v.optional(v.picklist(CRUD_ACTIONS)),
  name: v.optional(v.string()),
  response_is_list: v.boolean(),
  view: v.optional(v.string()),
});
export type RouteSchema = v.InferOutput<typeof RouteSchemaSchema>;

// A .Workflow()-declared transition off a Selection field — from/to/
// action_name always present, permission/condition present only when the
// transition declared them. condition is the raw domain-expression
// string, evaluated by the shell against the form's record.
export const WorkflowTransitionSchema = v.looseObject({
  from: v.string(),
  to: v.string(),
  action_name: v.string(),
  permission: v.optional(v.string()),
  condition: v.optional(v.string()),
});
export type WorkflowTransition = v.InferOutput<typeof WorkflowTransitionSchema>;

// A Selection field's .Workflow() declaration — present on FieldDef.workflow
// only when that field actually declared one (most fields, even most
// Selection fields, don't).
export const FieldWorkflowSchema = v.looseObject({
  states: v.array(v.string()),
  transitions: v.array(WorkflowTransitionSchema),
});
export type FieldWorkflow = v.InferOutput<typeof FieldWorkflowSchema>;

// shell-architecture.md §9's FieldDef.
export const FieldDefSchema = v.looseObject({
  name: v.string(),
  type: v.string(),
  required: v.optional(v.boolean()),
  related_model: v.optional(v.string()),
  // "one2many" only — the many2one field on related_model pointing back
  // at this model.
  inverse_field: v.optional(v.string()),
  workflow: v.optional(FieldWorkflowSchema),
});
export type FieldDef = v.InferOutput<typeof FieldDefSchema>;

// shell-architecture.md §9's ModelDef.
export const ModelDefSchema = v.looseObject({
  name: v.string(),
  label: v.string(),
  label_plural: v.string(),
  fields: v.array(FieldDefSchema),
  enabled_ops: v.array(v.string()),
  shareable: v.boolean(),
  share_permissions: v.optional(v.array(v.picklist(["read", "write"]))),
});
export type ModelDef = v.InferOutput<typeof ModelDefSchema>;

export const ModuleSchemaSchema = v.looseObject({
  name: v.string(),
  version: v.string(),
  display_name: v.string(),
  routes: v.array(RouteSchemaSchema),
  views: v.array(v.unknown()),
  navigation: v.array(v.unknown()),
  models: v.record(v.string(), ModelDefSchema),
  permissions: v.array(v.unknown()),
  frontend: v.nullable(v.looseObject({ bundle_url: v.string(), bundle_sha256: v.string() })),
  public_config: v.record(v.string(), v.unknown()),
});
export type ModuleSchema = v.InferOutput<typeof ModuleSchemaSchema>;

export const MetaSchemaSchema = v.looseObject({
  modules: v.record(v.string(), ModuleSchemaSchema),
  engine_version: v.string(),
  schema_hash: v.string(),
});
export type MetaSchema = v.InferOutput<typeof MetaSchemaSchema>;
