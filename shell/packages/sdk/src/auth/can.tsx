import type { ReactNode } from "react";
import { usePermission } from "./use-permission.js";

export interface CanProps {
  do: string;
  on?: string;
  not?: boolean;
  fallback?: ReactNode;
  children: ReactNode;
}

export function shouldRenderCan(allowed: boolean, not: boolean): boolean {
  return not ? !allowed : allowed;
}

export function Can({ do: permission, on, not = false, fallback = null, children }: CanProps): ReactNode {
  const allowed = usePermission(permission, on);
  return shouldRenderCan(allowed, not) ? children : fallback;
}
