import { ActionButton, Icon, PageLayout } from "@goerp/sdk/components";
import { type AppError, isAppError } from "@goerp/sdk/error";
import { useOnline } from "@goerp/sdk/realtime";
import type { ErrorComponentProps } from "@tanstack/react-router";
import { WifiOff } from "lucide-react";
import type { ReactNode } from "react";
import { ServerErrorPage } from "./server-error-page.js";

export function isServerFailure(error: unknown): error is AppError {
  return isAppError(error) && error.isServerError();
}

// The router's default errorComponent. Any failure while offline renders the offline message in
// place, at the requested URL. Online, a 5xx AppError gets the chrome-free 500 page; any other load
// failure keeps the chrome and gets the same inline treatment as form-renderer.tsx's load failure.
export function RouteError({ error, reset }: ErrorComponentProps): ReactNode {
  const online = useOnline();
  // A statically imported icon: lucide's dynamic icons load on demand, which fails offline.
  if (!online) {
    return (
      <PageLayout>
        <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
          <WifiOff size={20} className="text-text-secondary" aria-hidden="true" />
          <p className="text-text">This page isn't available offline.</p>
          <ActionButton variant="secondary" onClick={reset}>
            Try again
          </ActionButton>
        </div>
      </PageLayout>
    );
  }
  if (isServerFailure(error)) return <ServerErrorPage traceId={error.traceId} />;
  return (
    <PageLayout>
      <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
        <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
        <p className="text-text">Couldn't load this page.</p>
        <p className="text-sm text-text-secondary">
          {error instanceof Error ? error.message : "Something went wrong."}
        </p>
        <ActionButton variant="secondary" onClick={reset}>
          Retry
        </ActionButton>
      </div>
    </PageLayout>
  );
}
