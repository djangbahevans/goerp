import { useSyncExternalStore } from "react";

const STORAGE_KEY = "goerp-password-update-notice";

interface NoticeData {
  recommended: boolean;
  deadline: string | null;
}

const EMPTY_NOTICE: NoticeData = { recommended: false, deadline: null };

function read(): NoticeData {
  try {
    const stored = JSON.parse(window.sessionStorage.getItem(STORAGE_KEY) ?? "null") as NoticeData | null;
    if (stored?.recommended === true) {
      return { recommended: true, deadline: typeof stored.deadline === "string" ? stored.deadline : null };
    }
  } catch {
    // Blocked storage or malformed data leaves the notice in memory only.
  }
  return EMPTY_NOTICE;
}

export class PasswordUpdateNoticeStore {
  private notice = typeof window === "undefined" ? EMPTY_NOTICE : read();
  private readonly listeners = new Set<() => void>();

  get = (): boolean => this.notice.recommended;

  getSnapshot = (): NoticeData => this.notice;

  set(recommended: boolean, deadline: string | null = null): void {
    const next = { recommended, deadline: recommended ? deadline : null };
    if (next.recommended === this.notice.recommended && next.deadline === this.notice.deadline) return;
    this.notice = next;
    try {
      if (recommended) window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(next));
      else window.sessionStorage.removeItem(STORAGE_KEY);
    } catch {
      // Storage failures preserve the notice for this tab's lifetime.
    }
    for (const listener of this.listeners) listener();
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
}

export const passwordUpdateNotice = new PasswordUpdateNoticeStore();

export interface PasswordUpdateNotice extends NoticeData {
  dismiss: () => void;
}

export function usePasswordUpdateNotice(): PasswordUpdateNotice {
  const notice = useSyncExternalStore(
    passwordUpdateNotice.subscribe,
    passwordUpdateNotice.getSnapshot,
    () => EMPTY_NOTICE,
  );
  return { ...notice, dismiss: () => passwordUpdateNotice.set(false) };
}
