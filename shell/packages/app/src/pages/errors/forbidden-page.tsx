import { useAuth } from "@goerp/sdk/auth";
import { ViewRegistryContext } from "@goerp/sdk/schema";
import { type ReactNode, useContext } from "react";
import { ButtonLink } from "../../router/button-link.js";
import { ErrorLayout } from "./error-layout.js";

// shell-ux.md §5.3. Typed as a plain string because the admin pages aren't
// in the route tree yet.
const ADMIN_MODULES_PATH: string = "/admin/modules";

export type ForbiddenReason = "missing_permission" | "module_not_enabled";

export function isForbiddenReason(value: unknown): value is ForbiddenReason {
  return value === "missing_permission" || value === "module_not_enabled";
}

export interface ForbiddenPageProps {
  reason: ForbiddenReason;
  // The module's name, not its display name; used by module_not_enabled.
  module?: string | undefined;
}

// shell-ux.md §6.2.
export function ForbiddenPage({ reason, module }: ForbiddenPageProps): ReactNode {
  const isAdmin = useAuth().user?.roles.includes("admin") === true;
  const registry = useContext(ViewRegistryContext);

  if (reason === "missing_permission") {
    return (
      <ErrorLayout
        icon="shield-alert"
        heading="You don't have access to this page"
        description="Contact your administrator to request access."
        actions={
          <ButtonLink to="/" variant="primary">
            Go home
          </ButtonLink>
        }
      />
    );
  }

  const moduleName = (module && registry?.getModuleDisplayName(module)) || module || "This module";
  return (
    <ErrorLayout
      icon="package-x"
      heading="This module isn't enabled for your account"
      description={`${moduleName} needs to be enabled in your organisation's settings.`}
      actions={
        <>
          {isAdmin && (
            <ButtonLink to={ADMIN_MODULES_PATH} variant="primary">
              Go to settings
            </ButtonLink>
          )}
          <ButtonLink to="/" variant={isAdmin ? "secondary" : "primary"}>
            Go home
          </ButtonLink>
        </>
      }
    />
  );
}
