import { type AuthMachine, authMachine } from "../auth/auth-machine.js";
import type { AuthState } from "../auth/types.js";

// shell-architecture.md §12's WebSocketManager sketch.
const WS_PATH = "/_ws";
const INITIAL_RECONNECT_DELAY_MS = 1000;
const MAX_RECONNECT_DELAY_MS = 30_000;
// auth-internals.md's documented close code for "session became invalid" —
// a WebSocketManager receiving this never reconnects on its own; only a
// fresh login (via wireWebSocketManager) starts a new connection.
const AUTH_FAILURE_CLOSE_CODE = 4001;
const LOGOUT_CLOSE_CODE = 1000;

export interface RealtimeEnvelope {
  channel: string;
  type: string;
  payload?: unknown;
}

export type MessageHandler = (message: RealtimeEnvelope) => void;

export type WebSocketFactory = (url: string) => WebSocket;

function defaultWebSocketURL(): string {
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${location.host}${WS_PATH}`;
}

function parseEnvelope(data: unknown): RealtimeEnvelope | null {
  if (typeof data !== "string") return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return null;
  }
  if (
    typeof parsed !== "object" ||
    parsed === null ||
    typeof (parsed as { channel?: unknown }).channel !== "string" ||
    typeof (parsed as { type?: unknown }).type !== "string"
  ) {
    return null;
  }
  return parsed as RealtimeEnvelope;
}

// WebSocketManager is the shell's single persistent /_ws connection,
// shared by every channel subscriber. Each channel's subscriber set
// doubles as its ref count: the wire subscribe/unsubscribe message is
// only sent on the first subscriber / last unsubscriber, and channels
// with at least one subscriber are re-subscribed automatically after a
// reconnect.
export class WebSocketManager {
  private ws: WebSocket | null = null;
  private readonly channels = new Map<string, Set<MessageHandler>>();
  private reconnectDelay = INITIAL_RECONNECT_DELAY_MS;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private loggedOut = false;

  constructor(
    private readonly createSocket: WebSocketFactory = (url) => new WebSocket(url),
    private readonly url: () => string = defaultWebSocketURL,
  ) {}

  connect(): void {
    this.loggedOut = false;
    this.clearReconnectTimer();

    const ws = this.createSocket(this.url());
    this.ws = ws;

    ws.onopen = () => {
      this.reconnectDelay = INITIAL_RECONNECT_DELAY_MS;
      for (const channel of this.channels.keys()) {
        ws.send(JSON.stringify({ type: "subscribe", channel }));
      }
    };

    ws.onmessage = (event: MessageEvent) => {
      const message = parseEnvelope(event.data);
      if (!message) return;
      for (const handler of this.channels.get(message.channel) ?? []) {
        handler(message);
      }
    };

    ws.onclose = (event: CloseEvent) => {
      if (this.ws !== ws) return; // a stale handler from an already-superseded socket
      this.ws = null;
      if (this.loggedOut || event.code === AUTH_FAILURE_CLOSE_CODE) return;
      this.scheduleReconnect();
    };
  }

  // subscribe returns an unsubscribe function — the same shape
  // shell-architecture.md's own WebSocketManager.subscribe documents.
  subscribe(channel: string, handler: MessageHandler): () => void {
    let handlers = this.channels.get(channel);
    if (!handlers) {
      handlers = new Set();
      this.channels.set(channel, handlers);
      this.ws?.send(JSON.stringify({ type: "subscribe", channel }));
    }
    handlers.add(handler);

    return () => {
      const current = this.channels.get(channel);
      if (!current?.delete(handler)) return;
      if (current.size === 0) {
        this.channels.delete(channel);
        this.ws?.send(JSON.stringify({ type: "unsubscribe", channel }));
      }
    };
  }

  disconnect(): void {
    this.loggedOut = true;
    this.clearReconnectTimer();
    this.ws?.close(LOGOUT_CLOSE_CODE, "logout");
    this.ws = null;
    this.channels.clear();
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private scheduleReconnect(): void {
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, this.reconnectDelay);
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, MAX_RECONNECT_DELAY_MS);
  }
}

// "refreshing" counts as authenticated here — the same definition
// AuthContextValue.isAuthenticated uses (auth-provider.tsx) — so a token
// refresh cycle (authenticated -> refreshing -> authenticated) never
// tears down the live connection. wireAutoRefresh's own narrower literal
// "authenticated" check doesn't apply here: that check's harmless no-op
// clear()-during-refresh doesn't have an equivalent here, since a real
// disconnect()/connect() pair is expensive and user-visible.
function isConnectedStatus(status: AuthState["status"]): boolean {
  return status === "authenticated" || status === "refreshing";
}

// Connects/disconnects in reaction to the auth machine's own state —
// session_expired can fire from http/api-client.ts's unrecoverable-401
// path, entirely outside auth-provider.tsx, so nothing short of watching
// the machine itself reliably catches every path in and out of
// "authenticated".
export function wireWebSocketManager(
  machine: AuthMachine,
  manager: WebSocketManager = new WebSocketManager(),
): WebSocketManager {
  let previousStatus = machine.getState().status;

  machine.subscribe(() => {
    const { status } = machine.getState();
    const wasConnected = isConnectedStatus(previousStatus);
    const isConnected = isConnectedStatus(status);

    if (isConnected && !wasConnected) {
      manager.connect();
    } else if (!isConnected && wasConnected) {
      manager.disconnect();
    }
    previousStatus = status;
  });

  return manager;
}

export const wsManager = wireWebSocketManager(authMachine);
