import { Badge } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import type { ModuleSummary } from "./admin-modules-api.js";

// Active (on), Disabled (the tenant turned an entitled module off), or Not on
// your plan (the plan does not include it).
export function ModuleStatusBadge({ module }: { module: ModuleSummary }): ReactNode {
  if (module.enabled) return <Badge label="Active" color="green" />;
  if (module.entitled) return <Badge label="Disabled" color="orange" />;
  return <Badge label="Not on your plan" color="gray" />;
}
