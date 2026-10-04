import { useAuth, usePasswordUpdateNotice } from "@goerp/sdk/auth";
import { useLocale } from "@goerp/sdk/i18n";
import type { ReactNode } from "react";
import { ChromeBanner } from "./chrome-banner.js";

export interface PasswordUpdateBannerProps {
  onDismissed: () => void;
}

export function PasswordUpdateBanner({ onDismissed }: PasswordUpdateBannerProps): ReactNode {
  const { recommended, deadline, dismiss } = usePasswordUpdateNotice();
  const { tenant, user } = useAuth();
  const { locale } = useLocale();
  const date = deadline ? new Date(deadline) : null;
  const formattedDeadline =
    date && !Number.isNaN(date.getTime())
      ? new Intl.DateTimeFormat(locale, {
          dateStyle: "long",
          timeZone: user?.timezone ?? tenant?.defaultTimezone ?? "UTC",
        }).format(date)
      : null;
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
      Your password doesn't meet {tenant?.name ?? "your organisation"}'s requirements. Update it to keep your account
      secure.
      {formattedDeadline && (
        <>
          {" "}
          You'll need to change it by {formattedDeadline} to keep using {tenant?.name ?? "your organisation"}.
        </>
      )}
    </ChromeBanner>
  );
}
