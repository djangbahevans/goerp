import type { APIClient } from "../http/types.js";
import { cacheUntilRejected } from "./cached-promise.js";
import type { MetaSchema } from "./types.js";

// Fetches and caches GET /_meta/schema for the process lifetime — a
// single shared instance (the exported `schemaRegistry` singleton) lets
// ActionRegistry and ResourceRegistry agree on one schema snapshot
// instead of each fetching it independently. Module-reload invalidation
// is the full view registry's concern (backlog #674, unfiled), not
// this one's.
export class SchemaRegistry {
  private readonly fetchSchema: () => Promise<MetaSchema>;

  constructor(client: Pick<APIClient, "get">) {
    this.fetchSchema = cacheUntilRejected(() => client.get<MetaSchema>("/_meta/schema"));
  }

  getSchema(): Promise<MetaSchema> {
    return this.fetchSchema();
  }
}
