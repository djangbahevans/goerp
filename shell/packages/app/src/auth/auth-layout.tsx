import type { ReactNode } from "react";

export interface AuthLayoutProps {
  children: ReactNode;
  tenantLogo?: { url: string; alt: string } | undefined;
  privacyPolicyUrl?: string | undefined;
}

// min-h-dvh (not h-dvh) lets content taller than the viewport grow the page
// and scroll it, while m-auto centers the card+footer group when it fits.
export function AuthLayout({ children, tenantLogo, privacyPolicyUrl }: AuthLayoutProps): ReactNode {
  return (
    <main className="flex min-h-dvh flex-col overflow-y-auto bg-bg px-4 py-8 text-text">
      <div className="m-auto w-full max-w-100">
        <div className="rounded-structural border border-border bg-surface p-8">
          {tenantLogo && <img src={tenantLogo.url} alt={tenantLogo.alt} className="mx-auto mb-4 h-8 w-auto" />}
          {children}
        </div>
        <footer className="mt-6 flex justify-center gap-3 text-xs text-text-secondary">
          <span>Powered by GoERP</span>
          {privacyPolicyUrl && (
            <a
              href={privacyPolicyUrl}
              className="rounded-control text-text hover:underline focus-visible:outline-none focus-visible:shadow-focus"
            >
              Privacy policy
            </a>
          )}
        </footer>
      </div>
    </main>
  );
}
