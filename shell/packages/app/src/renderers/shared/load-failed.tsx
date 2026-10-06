import { Button, Icon } from "@goerp/sdk/components";
import type { ReactNode } from "react";

export interface LoadFailedProps {
  message: string;
  // The underlying error's text, shown beneath the message.
  detail?: string | undefined;
  // Omitted when retrying can't help, such as a reference that doesn't exist.
  onRetry?: (() => void) | undefined;
}

// list-renderer.md's shared load-failed block: icon, message and "Retry".
export function LoadFailed({ message, detail, onRetry }: LoadFailedProps): ReactNode {
  return (
    <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
      <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
      <p className="text-text">{message}</p>
      {detail && <p className="text-sm text-text-secondary">{detail}</p>}
      {onRetry && (
        <Button variant="secondary" onClick={onRetry}>
          Retry
        </Button>
      )}
    </div>
  );
}
