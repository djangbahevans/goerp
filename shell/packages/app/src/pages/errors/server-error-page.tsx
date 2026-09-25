import { Button } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { ButtonLink } from "../../router/button-link.js";
import { ErrorLayout } from "./error-layout.js";

export interface ServerErrorPageProps {
  traceId: string | null;
  // Injectable for tests and stories; defaults to a full page reload.
  reload?: (() => void) | undefined;
}

// shell-ux.md §6.3. Rendered in place of the failed route, so "Try again"
// reloads the URL that failed.
export function ServerErrorPage({ traceId, reload = () => window.location.reload() }: ServerErrorPageProps): ReactNode {
  return (
    <ErrorLayout
      icon="unplug"
      heading="Something went wrong on our end"
      description="We've been notified and are working on it. Please try again."
      actions={
        <>
          <Button variant="primary" onClick={reload}>
            Try again
          </Button>
          <ButtonLink to="/">Go home</ButtonLink>
        </>
      }
      footnote={traceId === null ? undefined : `Error ref: ${traceId}`}
    />
  );
}
