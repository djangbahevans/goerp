import { useSyncExternalStore } from "react";
import { type SocketStatus, type WebSocketManager, wsManager } from "./ws-manager.js";

// A dropped socket counts only after this long, so an ordinary reconnect doesn't flash offline.
export const SOCKET_GRACE_MS = 5000;

export type OnlineListener = (online: boolean) => void;

export interface OnlineState {
  get(): boolean;
  subscribe(listener: OnlineListener): () => void;
}

// Combines the browser's offline report, which is only trustworthy when it says offline, with
// the WebSocket staying down past the grace period, which also catches a network without
// internet access and an unreachable engine.
export class Connectivity implements OnlineState {
  private browserOnline = true;
  private socketLost = false;
  private graceTimer: ReturnType<typeof setTimeout> | null = null;
  private online = true;
  private readonly listeners = new Set<OnlineListener>();

  get = (): boolean => this.online;

  subscribe = (listener: OnlineListener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  setBrowserOnline(online: boolean): void {
    this.browserOnline = online;
    this.update();
  }

  setSocketStatus(status: SocketStatus): void {
    if (status === "reconnecting") {
      this.graceTimer ??= setTimeout(() => {
        this.graceTimer = null;
        this.socketLost = true;
        this.update();
      }, SOCKET_GRACE_MS);
      return;
    }
    if (this.graceTimer !== null) {
      clearTimeout(this.graceTimer);
      this.graceTimer = null;
    }
    this.socketLost = false;
    this.update();
  }

  private update(): void {
    const online = this.browserOnline && !this.socketLost;
    if (online === this.online) return;
    this.online = online;
    for (const listener of this.listeners) listener(online);
  }
}

export function wireConnectivity(
  connectivity: Connectivity,
  manager: Pick<WebSocketManager, "getStatus" | "subscribeStatus" | "reconnectNow">,
  target: Pick<Window, "addEventListener"> & { navigator: Pick<Navigator, "onLine"> },
): Connectivity {
  connectivity.setBrowserOnline(target.navigator.onLine);
  connectivity.setSocketStatus(manager.getStatus());
  manager.subscribeStatus((status) => connectivity.setSocketStatus(status));
  target.addEventListener("offline", () => connectivity.setBrowserOnline(false));
  target.addEventListener("online", () => {
    connectivity.setBrowserOnline(true);
    manager.reconnectNow();
  });
  return connectivity;
}

const connectivity = new Connectivity();
if (typeof window !== "undefined") wireConnectivity(connectivity, wsManager, window);

// Whether the shell can reach the engine, outside React.
export const online: OnlineState = { get: connectivity.get, subscribe: connectivity.subscribe };

// Whether the shell can reach the engine. Pass `disabled={!online}` to controls that write (submit
// a form, run an action, start a mutation); controls that navigate, cancel or close stay enabled.
export function useOnline(): boolean {
  return useSyncExternalStore(online.subscribe, online.get, () => true);
}
