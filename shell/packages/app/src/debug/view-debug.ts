import { type APIExchange, apiClient, type ResponseObserver } from "@goerp/sdk";

export const VIEW_DEBUG_PARAM = "debug";
const VIEW_DEBUG_VALUE = "views";
const MAX_EXCHANGES = 50;
const MAX_CONDITIONS = 500;

// The engine environment comes from /_meta/schema, so a production build served by a
// development engine still enables the overlay and a development build served by a
// production engine never does.
export function isViewDebugActive(
  engineEnvironment: string | null | undefined,
  search: Readonly<Record<string, unknown>>,
): boolean {
  return engineEnvironment === "development" && search[VIEW_DEBUG_PARAM] === VIEW_DEBUG_VALUE;
}

export interface ConditionRecord {
  scope: string;
  location: string;
  expression: string;
  kind: "visible" | "readonly";
  recordId: string | null;
  result: boolean;
  error: string | null;
}

export function conditionKey(record: ConditionRecord): string {
  return JSON.stringify([record.scope, record.location, record.kind, record.expression, record.recordId]);
}

export interface ViewDebugSnapshot {
  conditions: readonly ConditionRecord[];
  exchanges: readonly APIExchange[];
}

const EMPTY_SNAPSHOT: ViewDebugSnapshot = { conditions: [], exchanges: [] };

// Collects what the overlay displays while view debugging is active, from before the
// overlay's own chunk loads, so the view's first data calls and conditions are kept.
export class ViewDebugRecorder {
  private stopObserving: (() => void) | null = null;
  private pathname: string | null = null;
  private readonly conditions = new Map<string, ConditionRecord>();
  private exchanges: APIExchange[] = [];
  private snapshot = EMPTY_SNAPSHOT;
  private readonly listeners = new Set<() => void>();
  private flushQueued = false;

  constructor(private readonly observer: ResponseObserver) {}

  get active(): boolean {
    return this.stopObserving !== null;
  }

  // Starts or stops recording; entering a new path discards the previous page's entries.
  sync(active: boolean, pathname: string): void {
    if (!active) {
      if (!this.stopObserving) return;
      this.stopObserving();
      this.stopObserving = null;
      this.pathname = null;
      this.clear();
      return;
    }
    this.stopObserving ??= this.observer.observeResponses((exchange) => {
      this.exchanges = [...this.exchanges.slice(1 - MAX_EXCHANGES), exchange];
      this.changed();
    });
    if (pathname !== this.pathname) {
      this.pathname = pathname;
      this.clear();
    }
  }

  // Called during render; listeners are notified in a microtask so no component
  // updates while another renders. Past the cap, only already-seen conditions update.
  recordCondition(record: ConditionRecord): void {
    if (!this.active) return;
    const key = conditionKey(record);
    const previous = this.conditions.get(key);
    if (!previous && this.conditions.size >= MAX_CONDITIONS) return;
    if (previous?.result === record.result && previous.error === record.error) return;
    this.conditions.set(key, record);
    this.changed();
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getSnapshot = (): ViewDebugSnapshot => this.snapshot;

  private clear(): void {
    this.conditions.clear();
    this.exchanges = [];
    this.changed();
  }

  private changed(): void {
    if (this.flushQueued) return;
    this.flushQueued = true;
    queueMicrotask(() => {
      this.flushQueued = false;
      this.snapshot = { conditions: [...this.conditions.values()], exchanges: this.exchanges };
      for (const listener of this.listeners) listener();
    });
  }
}

export const viewDebugRecorder = new ViewDebugRecorder(apiClient);
