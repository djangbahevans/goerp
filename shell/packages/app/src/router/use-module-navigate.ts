import type { NavigateFn } from "@goerp/sdk/react";
import { useNavigate } from "@tanstack/react-router";
import { useCallback } from "react";

export function useModuleNavigate(): NavigateFn {
  const navigate = useNavigate();
  return useCallback<NavigateFn>(
    (path, options) => void navigate({ href: path, replace: options?.replace ?? false }),
    [navigate],
  );
}
