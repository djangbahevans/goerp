export class SessionActivity {
  private version = 0;
  private acknowledged = 0;
  private detach: (() => void) | null = null;

  snapshot(): number {
    return this.version;
  }

  hasActivity(): boolean {
    return (
      this.version > this.acknowledged && (typeof document === "undefined" || document.visibilityState === "visible")
    );
  }

  record(): void {
    this.version += 1;
  }

  acknowledge(version = this.version): void {
    this.acknowledged = Math.max(this.acknowledged, version);
  }

  start(): void {
    if (this.detach || typeof document === "undefined") return;
    const events = ["pointerdown", "pointermove", "keydown", "touchstart", "wheel"];
    const input = (event: Event) => {
      if (event.isTrusted && document.visibilityState === "visible") this.record();
    };
    for (const event of events) document.addEventListener(event, input, { capture: true, passive: true });
    this.detach = () => {
      for (const event of events) document.removeEventListener(event, input, { capture: true });
    };
  }

  stop(): void {
    this.detach?.();
    this.detach = null;
    this.acknowledge();
  }
}

export const sessionActivity = new SessionActivity();
