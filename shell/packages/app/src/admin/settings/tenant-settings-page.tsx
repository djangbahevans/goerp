import { Button, PageHeader, PageLayout, SectionCard, Skeleton } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { EmailSection } from "./email-section.js";
import { GeneralSection } from "./general-section.js";
import { LocalisationSection } from "./localisation-section.js";
import { SecuritySection } from "./security-section.js";
import { useNotificationDelivery, useTenantSettings } from "./tenant-settings-api.js";

function LoadError({ what, onRetry }: { what: string; onRetry: () => void }): ReactNode {
  return (
    <div role="alert" className="flex flex-col items-start gap-2 text-sm">
      <span className="text-text">Couldn't load {what}.</span>
      <Button variant="secondary" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}

// shell-ux.md §5.5. Each section saves on its own; keying a section on its
// saved values resets its form whenever they change.
export function TenantSettingsPage(): ReactNode {
  const settings = useTenantSettings();
  const delivery = useNotificationDelivery();

  return (
    <PageLayout>
      <PageHeader title="Settings" subtitle="Your company's profile, email delivery, security and localisation." />
      {settings.isError ? (
        <LoadError what="your settings" onRetry={() => void settings.refetch()} />
      ) : !settings.data ? (
        <Skeleton type="card" />
      ) : (
        <div className="flex flex-col gap-6">
          <GeneralSection
            key={JSON.stringify(settings.data.general)}
            saved={settings.data.general}
            availableLocales={settings.data.localisation.availableLocales}
          />
          {delivery.isError ? (
            <SectionCard title="Email">
              <div className="mt-3">
                <LoadError what="your email settings" onRetry={() => void delivery.refetch()} />
              </div>
            </SectionCard>
          ) : !delivery.data ? (
            <Skeleton type="card" />
          ) : (
            <EmailSection
              key={JSON.stringify([delivery.data.email, delivery.data.locked])}
              saved={delivery.data.email}
              locked={delivery.data.locked}
            />
          )}
          <SecuritySection key={JSON.stringify(settings.data.security)} saved={settings.data.security} />
          <LocalisationSection
            key={JSON.stringify(settings.data.localisation)}
            saved={settings.data.localisation}
            defaultLocale={settings.data.general.defaultLocale}
          />
        </div>
      )}
    </PageLayout>
  );
}
