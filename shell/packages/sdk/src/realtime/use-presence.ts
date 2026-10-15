import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import * as v from "valibot";
import { authMachine } from "../auth/auth-machine.js";
import type { FieldOf, ResourceName } from "../names.js";
import { wsManager } from "./ws-manager.js";

export interface PresenceUser<R extends ResourceName = ResourceName> {
  userId: string;
  name: string;
  avatarUrl: string | null;
  editingField: FieldOf<R> | null;
  joinedAt: Date;
  lastSeenAt: Date;
}

export interface UsePresenceOptions<R extends ResourceName = ResourceName> {
  editingField?: FieldOf<R> | null;
}

const timestamp = v.pipe(
  v.string(),
  v.transform((value) => new Date(value)),
  v.check((date) => Number.isFinite(date.getTime())),
);
const presencePayload = v.object({
  resource: v.string(),
  record_id: v.string(),
  users: v.array(
    v.object({
      user_id: v.string(),
      name: v.string(),
      avatar_url: v.nullable(v.string()),
      editing_field: v.nullable(v.string()),
      joined_at: timestamp,
      last_seen_at: timestamp,
    }),
  ),
});

type PresenceSnapshot = Omit<PresenceUser, "editingField"> & { editingField: string | null };

interface Room {
  users: PresenceSnapshot[];
  listeners: Set<() => void>;
  focus: Map<symbol, string | null>;
  stop: () => void;
  dispose: () => void;
}

const rooms = new Map<string, Room>();
const empty: PresenceSnapshot[] = [];

function notify(room: Room): void {
  for (const listener of room.listeners) listener();
}

function subscribe(channel: string, listener: () => void): () => void {
  let room = rooms.get(channel);
  if (!room) {
    const created: Room = { users: empty, listeners: new Set(), focus: new Map(), stop: () => {}, dispose: () => {} };
    room = created;
    rooms.set(channel, created);
    let active = false;
    let terminal = false;
    const start = () => {
      active = true;
      const stopMessages = wsManager.subscribe(channel, (message) => {
        if (message.type === "subscription.denied" || message.type === "record.unavailable") {
          terminal = true;
          created.users = empty;
          created.stop();
          notify(created);
          return;
        }
        if (message.type !== "presence.update") return;
        const parsed = v.safeParse(presencePayload, message.payload);
        if (!parsed.success || `record:${parsed.output.resource}:${parsed.output.record_id}` !== channel) return;
        created.users = parsed.output.users.map((user) => ({
          userId: user.user_id,
          name: user.name,
          avatarUrl: user.avatar_url,
          editingField: user.editing_field,
          joinedAt: user.joined_at,
          lastSeenAt: user.last_seen_at,
        }));
        notify(created);
      });
      const leave = wsManager.joinPresence(channel);
      const stopReconnect = wsManager.subscribeReconnect(() => {
        created.users = empty;
        notify(created);
      });
      created.stop = () => {
        active = false;
        stopMessages();
        leave();
        stopReconnect();
      };
      if (created.focus.size > 0) reportFocus(channel, created);
    };
    start();
    const stopAuth = authMachine.subscribe(() => {
      const state = authMachine.getState();
      if ((state.status === "authenticated" || state.status === "refreshing") && !state.user.passwordChangeRequired) {
        if (!active && !terminal) start();
        return;
      }
      created.users = empty;
      created.stop();
      notify(created);
    });
    created.dispose = () => {
      created.stop();
      stopAuth();
    };
  }
  room.listeners.add(listener);
  return () => {
    room.listeners.delete(listener);
    if (room.listeners.size === 0) {
      room.dispose();
      rooms.delete(channel);
    }
  };
}

function reportFocus(channel: string, room: Room): void {
  wsManager.setPresenceFocus(channel, [...room.focus.values()].at(-1) ?? null);
}

export function usePresence<R extends ResourceName>(
  resource: R,
  id: string,
  options: UsePresenceOptions<R> = {},
): PresenceUser<R>[] {
  return useRecordPresence(resource, id, options);
}

// An undefined id joins nothing, for record hooks that also serve create mode.
export function useRecordPresence<R extends ResourceName>(
  resource: R,
  id: string | undefined,
  options: UsePresenceOptions<R> = {},
): PresenceUser<R>[] {
  const channel = id === undefined ? null : `record:${resource}:${id}`;
  const [owner] = useState(() => Symbol());
  const listen = useCallback(
    (listener: () => void) => (channel === null ? () => {} : subscribe(channel, listener)),
    [channel],
  );
  const snapshot = useCallback(() => (channel === null ? empty : (rooms.get(channel)?.users ?? empty)), [channel]);
  const users = useSyncExternalStore(listen, snapshot, () => empty);

  useEffect(
    () => () => {
      if (channel === null) return;
      const room = rooms.get(channel);
      if (room?.focus.delete(owner)) reportFocus(channel, room);
    },
    [channel, owner],
  );

  const { editingField } = options;
  useEffect(() => {
    if (channel === null) return;
    const room = rooms.get(channel);
    if (!room) return;
    // Reinsert so the most recent local focus change wins on the shared connection.
    const hadFocus = room.focus.delete(owner);
    if (editingField !== undefined) room.focus.set(owner, editingField);
    if (hadFocus || editingField !== undefined) reportFocus(channel, room);
  }, [channel, editingField, owner]);

  return users as PresenceUser<R>[];
}
