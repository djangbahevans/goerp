import { usePasswordUpdateNotice } from "@goerp/sdk/auth";
import type { ReactNode } from "react";
import { ChromeBanner } from "./chrome-banner.js";

export interface PasswordUpdateBannerProps {
  onDismissed: () => void;
}

// components/chrome-banner.md "PasswordUpdateBanner" (shell-ux.md §2.1).
export function PasswordUpdateBanner({ onDismissed }: PasswordUpdateBannerProps): ReactNode {
  const { recommended, dismiss } = usePasswordUpdateNotice();
  if (!recommended) return null;

  return (
    <ChromeBanner
      tone="warning"
      action={{ label: "Update password", to: "/settings/profile", hash: "change-password" }}
      onDismiss={() => {
        dismiss();
        onDismissed();
      }}
      dismissLabel="Dismiss password notice"
    >
      Your organization has updated its password requirements. Update your password to keep your account secure.
    </ChromeBanner>
  );
}
