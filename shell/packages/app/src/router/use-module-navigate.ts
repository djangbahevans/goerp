import type { NavigateFn } from "@goerp/sdk/react";
import { useNavigate } from "@tanstack/react-router";
import { useCallback } from "react";

// useModule's navigate (typescript-sdk-reference.md §5): a module passes a
// plain path, query string included, so it goes to the router as an href.
export function useModuleNavigate(): NavigateFn {
  const navigate = useNavigate();
  return useCallback<NavigateFn>(
    (path, options) => void navigate({ href: path, replace: options?.replace ?? false }),
    [navigate],
  );
}
