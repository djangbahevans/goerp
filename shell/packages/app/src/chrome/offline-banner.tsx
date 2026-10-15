import { useOnline } from "@goerp/sdk/realtime";
import type { ReactNode } from "react";
import { ChromeBanner } from "./chrome-banner.js";

// Clears itself when the connection returns, so it has no dismiss button.
export function OfflineBanner(): ReactNode {
  if (useOnline()) return null;
  return <ChromeBanner tone="warning">You're offline. Changes can't be saved until you reconnect.</ChromeBanner>;
}
