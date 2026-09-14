// shell-architecture.md §9/§16: the shape a module manifest's `navigation`
// array declares, merged across modules by `viewRegistry.navigationTree`.
// SDK-owned (packages/sdk/src/schema/view-registry.ts) like every other
// schema-derived type — re-exported here so chrome/* call sites don't need
// to know that.
export type { NavigationGroup, NavigationItem } from "@goerp/sdk/schema";
