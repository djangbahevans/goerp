import { useSyncExternalStore } from "react";

// The password_update_recommended sign-in flag (auth-internals.md §3
// "Password policy versioning"), kept per tab so it survives a reload: the
// GET /auth/me session check a reload runs doesn't repeat it
// (components/chrome-banner.md "PasswordUpdateBanner"). Same key convention
// as use-theme.ts's "goerp-theme".
const STORAGE_KEY = "goerp-password-update-recommended";

type Listener = () => void;

function read(): boolean {
  try {
    return window.sessionStorage.getItem(STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

// Module-level singleton, the same shape as use-locale.ts's LocaleStore.
// Storage failures (private mode, blocked site data) degrade to an
// in-memory flag for the tab's lifetime.
export class PasswordUpdateNoticeStore {
  private recommended = typeof window === "undefined" ? false : read();
  private readonly listeners = new Set<Listener>();

  get = (): boolean => this.recommended;

  set(recommended: boolean): void {
    if (recommended === this.recommended) return;
    this.recommended = recommended;
    try {
      if (recommended) window.sessionStorage.setItem(STORAGE_KEY, "1");
      else window.sessionStorage.removeItem(STORAGE_KEY);
    } catch {
      // Kept in memory only.
    }
    for (const listener of this.listeners) listener();
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
}

export const passwordUpdateNotice = new PasswordUpdateNoticeStore();

export interface PasswordUpdateNotice {
  recommended: boolean;
  dismiss: () => void;
}

// usePasswordUpdateNotice reports whether this tab's sign-in carried
// password_update_recommended and the user hasn't since dismissed it,
// changed their password, or signed out.
export function usePasswordUpdateNotice(): PasswordUpdateNotice {
  const recommended = useSyncExternalStore(passwordUpdateNotice.subscribe, passwordUpdateNotice.get, () => false);
  return { recommended, dismiss: () => passwordUpdateNotice.set(false) };
}
