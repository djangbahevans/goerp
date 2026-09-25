import { type ToastAPI, toast } from "../notifications/toast.js";

export interface UseToastResult {
  toast: ToastAPI;
}

const result: UseToastResult = { toast };

// shell-ux.md §7.1: the shell-wide toast API, the same instance as the
// `toast` export of @goerp/sdk/notifications.
export function useToast(): UseToastResult {
  return result;
}
