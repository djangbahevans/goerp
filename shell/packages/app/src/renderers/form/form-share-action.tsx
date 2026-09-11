import { actionButtonClassName } from "@goerp/sdk/components";
import { modelRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";

// Shown when the model declares .Shareable(), gated off the model
// registry. The share-management panel itself is goerp#476's scope — this
// only gives the trigger and the panel shell a real treatment.
export function ShareHeaderAction({ resource, recordId }: { resource: string; recordId: string | undefined }) {
  const { data: model } = useQuery({
    queryKey: ["form-model-shareable", resource],
    queryFn: () => modelRegistry.resolve(resource),
  });
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const containerRef = useRef<HTMLSpanElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);

  // Same disclosure dismissal ActionMenu's own panel uses — outside click
  // or Escape closes it, Escape stops there rather than also closing an
  // ancestor Escape-closable overlay, and closing returns focus to the
  // trigger either way.
  useEffect(() => {
    if (!open) return;
    function handlePointerDown(event: MouseEvent): void {
      if (containerRef.current?.contains(event.target as Node)) return;
      setOpen(false);
    }
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      setOpen(false);
      triggerRef.current?.focus();
    }
    document.addEventListener("mousedown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  if (!model?.shareable || recordId === undefined) return null;

  return (
    <span ref={containerRef} className="relative inline-block">
      <button
        ref={triggerRef}
        type="button"
        className={actionButtonClassName("ghost", "md")}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((v) => !v)}
      >
        Share
      </button>
      {open && (
        <span
          id={panelId}
          role="dialog"
          className="absolute top-full right-0 z-(--z-dropdown) mt-1 min-w-40 max-w-70 rounded-structural border border-border bg-surface p-3 text-sm text-text-secondary shadow-md"
        >
          Share management — see goerp#476.
        </span>
      )}
    </span>
  );
}
