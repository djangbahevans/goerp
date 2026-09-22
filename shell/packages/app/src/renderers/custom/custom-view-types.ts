import { optionalNullable as opt } from "@goerp/sdk/schema";
import * as v from "valibot";

// manifest-spec.md §9's Custom View wire schema (view-system.md §9
// "Declaring a custom view") — the common view fields plus a required
// `component`, resolved through componentRegistry by CustomViewRenderer.
export const CustomViewDeclarationSchema = v.looseObject({
  name: v.string(),
  type: v.literal("custom"),
  resource: v.string(),
  label: v.string(),
  icon: opt(v.string()),
  permission: opt(v.string()),
  component: v.string(),
});
export type CustomViewDeclaration = v.InferOutput<typeof CustomViewDeclarationSchema>;
