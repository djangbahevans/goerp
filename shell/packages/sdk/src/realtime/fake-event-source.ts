import { act } from "@testing-library/react";

// Follows the browser's EventSource: a closed source dispatches nothing,
// and a failed connection is either retried (CONNECTING) or given up on
// (CLOSED) before its error event fires.
export class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];

  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  readyState = FakeEventSource.CONNECTING;
  private readonly listeners = new Map<string, () => void>();

  constructor(
    readonly url: string,
    readonly init?: EventSourceInit,
  ) {
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: () => void) {
    this.listeners.set(type, listener);
  }

  close() {
    this.readyState = FakeEventSource.CLOSED;
  }

  open() {
    this.dispatch(() => {
      this.readyState = FakeEventSource.OPEN;
      this.onopen?.();
    });
  }

  message(data: unknown) {
    this.dispatch(() => this.onmessage?.({ data: JSON.stringify(data) }));
  }

  done() {
    this.dispatch(() => this.listeners.get("done")?.());
  }

  drop(): Event {
    return this.fail(FakeEventSource.CONNECTING);
  }

  refuse(): Event {
    return this.fail(FakeEventSource.CLOSED);
  }

  private fail(readyState: number): Event {
    const event = new Event("error");
    this.dispatch(() => {
      this.readyState = readyState;
      this.onerror?.(event);
    });
    return event;
  }

  private dispatch(deliver: () => void) {
    if (this.readyState === FakeEventSource.CLOSED) return;
    act(deliver);
  }
}

export function source(index = FakeEventSource.instances.length - 1) {
  const instance = FakeEventSource.instances[index];
  if (!instance) throw new Error(`no EventSource #${index}`);
  return instance;
}
