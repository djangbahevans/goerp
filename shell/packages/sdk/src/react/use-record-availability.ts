import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { AppError } from "../error/app-error.js";
import { useRealtimeChannel } from "../realtime/use-realtime-channel.js";
import { recordQueryKey } from "./record-query-key.js";

class RecordSession {
  readonly controller = new AbortController();
  private active = true;

  constructor(
    readonly resource: string,
    readonly id: string | undefined,
  ) {}

  activate() {
    this.active = true;
  }
  deactivate() {
    this.active = false;
  }
  invalidate() {
    this.controller.abort();
  }
  isAvailable = () => this.active && !this.controller.signal.aborted;
  assertAvailable = () => {
    if (!this.isAvailable()) {
      throw new AppError({ code: "record.unavailable", message: "This record is no longer available", httpStatus: 0 });
    }
  };
}

export function useRecordAvailability(resource: string, id: string | undefined) {
  const queryClient = useQueryClient();
  const session = useMemo(() => new RecordSession(resource, id), [resource, id]);
  const [unavailableSession, setUnavailableSession] = useState<typeof session>();

  useEffect(() => {
    session.activate();
    return () => {
      session.deactivate();
    };
  }, [session]);

  useRealtimeChannel(
    `record:${resource}:${id ?? ""}`,
    (message) => {
      if (
        message.type !== "record.unavailable" ||
        message.resource !== resource ||
        message.recordId !== id ||
        !session.isAvailable()
      )
        return;
      session.invalidate();
      setUnavailableSession(session);
      const queryKey = recordQueryKey(resource, id);
      void queryClient.cancelQueries({ queryKey, exact: true });
      queryClient.removeQueries({ queryKey, exact: true });
    },
    { enabled: id !== undefined && unavailableSession !== session },
  );

  return {
    isUnavailable: unavailableSession === session,
    isAvailable: session.isAvailable,
    assertAvailable: session.assertAvailable,
    signal: session.controller.signal,
  };
}
