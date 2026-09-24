import type { ReactNode, RefObject } from "react";
import { PasswordUpdateBanner } from "./password-update-banner.js";

export interface ChromeBannersProps {
  mainRef: RefObject<HTMLElement | null>;
}

// components/chrome-banner.md: the session notices in fixed order
// (connectivity first, then account). The wrapper stays mounted while empty
// so a banner that appears mid-session lands in an already-rendered
// container and is announced.
export function ChromeBanners({ mainRef }: ChromeBannersProps): ReactNode {
  // A dismissed banner unmounts with focus on its button; hand focus to
  // <main>, the skip link's target, rather than dropping it to <body>.
  const focusMain = () => mainRef.current?.focus();

  return (
    <div className="shrink-0">
      <PasswordUpdateBanner onDismissed={focusMain} />
    </div>
  );
}
