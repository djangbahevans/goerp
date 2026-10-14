import { defineModule, type ModuleDefinition } from "@goerp/sdk/module";
import { type MetaSchema, MetaSchemaSchema } from "@goerp/sdk/schema";
import * as v from "valibot";

const ParameterSchema = v.object({
  name: v.string(),
  source: v.picklist(["path", "body", "query", "list_query", "etag"]),
  wire_name: v.optional(v.string()),
});

const FixtureSchema = v.object({
  module: v.string(),
  schema: MetaSchemaSchema,
  functions: v.record(
    v.string(),
    v.object({
      method: v.string(),
      path: v.string(),
      parameters: v.array(ParameterSchema),
      response: v.picklist(["value", "paged", "void"]),
    }),
  ),
});

export type ViewTestFixture = v.InferOutput<typeof FixtureSchema>;
export type TestingFunction = ViewTestFixture["functions"][string];
const configurations = new Map<string, ViewTestFixture>();

export function configureViewTests({ fixture, module }: { fixture: unknown; module: ModuleDefinition }): void {
  const parsed = v.parse(FixtureSchema, fixture);
  if (parsed.module !== module.name || !parsed.schema.modules[module.name]) {
    throw new Error(`Testing fixture does not describe module "${module.name}"`);
  }
  defineModule(module);
  configurations.set(module.name, parsed);
}

export function viewTestConfiguration(module: string): { fixture: ViewTestFixture; schema: MetaSchema } {
  const fixture = configurations.get(module);
  if (!fixture) throw new Error(`Call configureViewTests with the generated fixture for "${module}" first`);
  return { fixture, schema: fixture.schema };
}
