import { Button } from "@goerp/sdk/components";
import { type ReactNode, useEffect, useRef } from "react";
import { AuthLayout } from "./auth-layout.js";

// A path on the shared-domain host, or null when the engine sent no usable
// app URL.
export function sharedHostURL(appUrl: string | null, path: string): string | null {
  if (appUrl === null) return null;
  try {
    return new URL(path, appUrl).href;
  } catch {
    return null;
  }
}

export interface WorkspaceNotFoundProps {
  appUrl: string | null;
  // The browser's current hostname; tests substitute it.
  hostname?: string | undefined;
}

// shell-ux.md §2.1 "Workspace not found".
export function WorkspaceNotFound({ appUrl, hostname = window.location.hostname }: WorkspaceNotFoundProps): ReactNode {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const loginHref = sharedHostURL(appUrl, "/auth/login");

  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  return (
    <AuthLayout>
      <div className="flex flex-col gap-4">
        <h1 ref={headingRef} tabIndex={-1} className="font-semibold text-text text-xl focus:outline-none">
          Workspace not found
        </h1>
        <p className="text-sm text-text-secondary">
          There's no workspace at <strong className="break-all font-semibold text-text">{hostname}</strong>. Check the
          address, or sign in to find yours.
        </p>
        {loginHref && (
          <Button href={loginHref} variant="primary" fullWidth>
            Go to sign in
          </Button>
        )}
      </div>
    </AuthLayout>
  );
}
