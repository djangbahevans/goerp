import { useCallback, useEffect, useEffectEvent, useRef, useState } from "react";

export type SSEStatus = "connecting" | "connected" | "done" | "error";

export interface UseSSEOptions<T> {
  // false opens no connection.
  enabled?: boolean | undefined;
  onMessage?: ((data: T) => void) | undefined;
  onDone?: (() => void) | undefined;
  onError?: ((err: Event) => void) | undefined;
}

export interface UseSSEResult<T> {
  data: T | null;
  status: SSEStatus;
  close: () => void;
}

// typescript-sdk-reference.md §7 "useSSE". The connection follows url and
// enabled only; each event goes to the callbacks of the latest render.
export function useSSE<T>(url: string, options: UseSSEOptions<T> = {}): UseSSEResult<T> {
  const { enabled = true } = options;
  const onMessage = useEffectEvent((data: T) => options.onMessage?.(data));
  const onDone = useEffectEvent(() => options.onDone?.());
  const onError = useEffectEvent((err: Event) => options.onError?.(err));

  const [status, setStatus] = useState<SSEStatus>("connecting");
  const [data, setData] = useState<T | null>(null);
  const sourceRef = useRef<EventSource | null>(null);

  // Reset while rendering, so the first render for a new stream never
  // reports the previous stream's status or data.
  const [stream, setStream] = useState({ url, enabled });
  if (stream.url !== url || stream.enabled !== enabled) {
    setStream({ url, enabled });
    setStatus("connecting");
    setData(null);
  }

  useEffect(() => {
    if (!enabled) return;
    // EventSource cannot send an Authorization header; the session cookie
    // authenticates the stream.
    const source = new EventSource(url, { withCredentials: true });
    sourceRef.current = source;

    source.onopen = () => setStatus("connected");
    source.onmessage = (event: MessageEvent<string>) => {
      const parsed = JSON.parse(event.data) as T;
      setData(parsed);
      onMessage(parsed);
    };
    // Closing here stops the browser from reconnecting to a finished stream.
    source.addEventListener("done", () => {
      source.close();
      setStatus("done");
      onDone();
    });
    source.onerror = (event) => {
      // CONNECTING: the browser is retrying a dropped connection.
      // CLOSED: the route refused the stream, and the browser has given up.
      if (source.readyState !== EventSource.CLOSED) {
        setStatus("connecting");
        return;
      }
      setStatus("error");
      onError(event);
    };

    return () => {
      source.close();
      sourceRef.current = null;
    };
  }, [url, enabled]);

  const close = useCallback(() => {
    sourceRef.current?.close();
    setStatus("done");
  }, []);

  return { data, status, close };
}
