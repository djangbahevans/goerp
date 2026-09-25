import { Icon, type IconNameLike } from "@goerp/sdk/components";
import type { CSSProperties, ReactNode } from "react";

export interface ErrorLayoutProps {
  icon: IconNameLike;
  iconStyle?: CSSProperties | undefined;
  heading: string;
  description: string;
  actions: ReactNode;
  // Small print under the actions, e.g. the 500 page's error reference.
  footnote?: ReactNode | undefined;
}

// shell-ux.md §6: every error page's shared, chrome-free frame.
export function ErrorLayout({ icon, iconStyle, heading, description, actions, footnote }: ErrorLayoutProps): ReactNode {
  return (
    <main className="flex min-h-dvh flex-col items-center justify-center bg-bg px-4 py-8 text-center text-text">
      <Icon
        name={icon}
        size={48}
        strokeWidth={1.5}
        className="text-text-secondary"
        style={iconStyle}
        aria-hidden="true"
      />
      <h1 className="mt-6 font-semibold text-text text-xl">{heading}</h1>
      <p className="mt-2 max-w-sm text-sm text-text-secondary">{description}</p>
      <div className="mt-6 flex flex-wrap justify-center gap-3">{actions}</div>
      {footnote !== undefined && <p className="mt-6 text-text-secondary text-xs">{footnote}</p>}
    </main>
  );
}
