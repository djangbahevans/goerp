import * as v from "valibot";
import type { APIClient } from "../http/types.js";
import { cacheUntilRejected } from "./cached-promise.js";
import { summarizeIssues } from "./summarize-issues.js";
import { type MetaSchema, MetaSchemaSchema } from "./types.js";

// Every registry in this module (ActionRegistry, ResourceRegistry, ...)
// ultimately reads through this one fetch, so validating the raw response
// here — once — turns a malformed field into one clear error at the
// source instead of a confusing crash wherever some unrelated consumer
// first touches the bad field.

// Fetches and caches GET /_meta/schema for the process lifetime — a
// single shared instance (the exported `schemaRegistry` singleton) lets
// ActionRegistry and ResourceRegistry agree on one schema snapshot
// instead of each fetching it independently. Module-reload invalidation
// is the full view registry's concern (backlog #674, unfiled), not
// this one's.
export class SchemaRegistry {
  private readonly fetchSchema: () => Promise<MetaSchema>;

  constructor(client: Pick<APIClient, "get">) {
    this.fetchSchema = cacheUntilRejected(async () => {
      const raw = await client.get<unknown>("/_meta/schema");
      const result = v.safeParse(MetaSchemaSchema, raw);
      if (!result.success) {
        throw new Error(
          `SchemaRegistry: GET /_meta/schema returned a malformed response — ${summarizeIssues(result.issues)}`,
        );
      }
      return result.output;
    });
  }

  getSchema(): Promise<MetaSchema> {
    return this.fetchSchema();
  }
}
