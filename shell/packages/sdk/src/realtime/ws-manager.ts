import { type AuthMachine, authMachine } from "../auth/auth-machine.js";
import type { AuthState } from "../auth/types.js";

const WS_PATH = "/_ws";
const INITIAL_RECONNECT_DELAY_MS = 1000;
const MAX_RECONNECT_DELAY_MS = 30_000;
const AUTH_FAILURE_CLOSE_CODE = 4001;
const LOGOUT_CLOSE_CODE = 1000;
// WebSocket.readyState's spec-fixed OPEN value, used as a literal so this
// doesn't depend on a global WebSocket constructor being present.
const READY_STATE_OPEN = 1;

export interface RealtimeEnvelope {
  channel: string;
  type: string;
  payload?: unknown;
}

export type MessageHandler = (message: RealtimeEnvelope) => void;
export type ReconnectHandler = (channels: ReadonlySet<string>) => void;

export type WebSocketFactory = (url: string) => WebSocket;

export function tenantChannel(tenantId: string): string {
  return `tenant:${tenantId}`;
}

export function userChannel(userId: string): string {
  return `ui:user:${userId}`;
}

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

// Subscription IDs keep registrations independent when multiple subscribers
// use the same handler function.
export class WebSocketManager {
  private ws: WebSocket | null = null;
  private readonly channels = new Map<string, Map<number, MessageHandler>>();
  private nextSubscriptionId = 0;
  private reconnectDelay = INITIAL_RECONNECT_DELAY_MS;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private loggedOut = false;
  private hasOpened = false;
  private readonly reconnectHandlers = new Map<number, ReconnectHandler>();
  private readonly presence = new Map<string, Set<number>>();

  // An injectable callback lets standalone managers handle authentication failures
  // without changing the shared session.
  onAuthFailure: (() => void) | null = null;

  constructor(
    private readonly createSocket: WebSocketFactory = (url) => new WebSocket(url),
    private readonly url: () => string = defaultWebSocketURL,
  ) {}

  connect(): void {
    this.loggedOut = false;
    this.clearReconnectTimer();

    // Detach before closing so the old socket's onclose sees itself as
    // already-stale, instead of triggering a spurious second reconnect.
    const oldWs = this.ws;
    this.ws = null;
    oldWs?.close();

    const ws = this.createSocket(this.url());
    this.ws = ws;

    ws.onopen = () => {
      if (this.ws !== ws) return;
      const isReconnect = this.hasOpened;
      this.hasOpened = true;
      this.reconnectDelay = INITIAL_RECONNECT_DELAY_MS;
      for (const channel of this.channels.keys()) {
        ws.send(JSON.stringify({ type: "subscribe", channel }));
      }
      for (const channel of this.presence.keys()) {
        this.send({ type: "presence.join", channel });
      }
      if (isReconnect) {
        const channels = new Set(this.channels.keys());
        for (const handler of this.reconnectHandlers.values()) handler(channels);
      }
    };

    ws.onmessage = (event: MessageEvent) => {
      if (this.ws !== ws) return;
      const message = parseEnvelope(event.data);
      if (message) this.dispatch(message);
    };

    ws.onclose = (event: CloseEvent) => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (event.code === AUTH_FAILURE_CLOSE_CODE) {
        this.onAuthFailure?.();
        return;
      }
      if (this.loggedOut) return;
      this.scheduleReconnect();
    };
  }

  subscribeReconnect(handler: ReconnectHandler): () => void {
    const id = this.nextSubscriptionId++;
    this.reconnectHandlers.set(id, handler);
    return () => {
      this.reconnectHandlers.delete(id);
    };
  }

  joinPresence(channel: string): () => void {
    const unsubscribe = this.subscribe(channel, () => {});
    let members = this.presence.get(channel);
    if (!members) {
      members = new Set();
      this.presence.set(channel, members);
      this.send({ type: "presence.join", channel });
    }
    const id = this.nextSubscriptionId++;
    members.add(id);

    return () => {
      const current = this.presence.get(channel);
      if (!current?.delete(id)) return;
      if (current.size === 0) {
        this.presence.delete(channel);
        this.send({ type: "presence.leave", channel });
      }
      unsubscribe();
    };
  }

  dispatch(message: RealtimeEnvelope): void {
    for (const handler of this.channels.get(message.channel)?.values() ?? []) {
      handler(message);
    }
  }

  // Channels registered while CONNECTING are sent by onopen; sending them
  // immediately would throw InvalidStateError.
  subscribe(channel: string, handler: MessageHandler): () => void {
    let subscribers = this.channels.get(channel);
    if (!subscribers) {
      subscribers = new Map();
      this.channels.set(channel, subscribers);
      this.send({ type: "subscribe", channel });
    }
    const id = this.nextSubscriptionId++;
    subscribers.set(id, handler);

    return () => {
      const current = this.channels.get(channel);
      if (!current?.delete(id)) return;
      if (current.size === 0) {
        this.channels.delete(channel);
        this.send({ type: "unsubscribe", channel });
      }
    };
  }

  private send(message: unknown): void {
    if (this.ws?.readyState === READY_STATE_OPEN) {
      this.ws.send(JSON.stringify(message));
    }
  }

  disconnect(): void {
    this.loggedOut = true;
    this.clearReconnectTimer();
    this.ws?.close(LOGOUT_CLOSE_CODE, "logout");
    this.ws = null;
    this.channels.clear();
    this.presence.clear();
    this.hasOpened = false;
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

function canConnect(state: AuthState): boolean {
  return (state.status === "authenticated" || state.status === "refreshing") && !state.user.passwordChangeRequired;
}

export function wireWebSocketManager(
  machine: AuthMachine,
  manager: WebSocketManager = new WebSocketManager(),
): WebSocketManager {
  manager.onAuthFailure = () => {
    machine.transition({ type: "session_expired" });
  };

  let wasConnected = canConnect(machine.getState());

  machine.subscribe(() => {
    const isConnected = canConnect(machine.getState());

    if (isConnected && !wasConnected) {
      manager.connect();
    } else if (!isConnected && wasConnected) {
      manager.disconnect();
    }
    wasConnected = isConnected;
  });

  return manager;
}

export const wsManager = wireWebSocketManager(authMachine);
