import { actionButtonClassName } from "@goerp/sdk/components";
import { modelRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import { SharePanel } from "./share-panel.js";

// Shown when the model declares .Shareable(), gated off the model
// registry.
export function ShareHeaderAction({ resource, recordId }: { resource: string; recordId: string | undefined }) {
  const { data: model } = useQuery({
    queryKey: ["form-model-shareable", resource],
    queryFn: () => modelRegistry.resolve(resource),
  });
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const headingId = `${panelId}-heading`;
  const containerRef = useRef<HTMLDivElement | null>(null);
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
    <div ref={containerRef} className="relative inline-block">
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
        <div
          id={panelId}
          role="dialog"
          aria-labelledby={headingId}
          className="absolute top-full right-0 z-(--z-dropdown) mt-1 w-90 max-w-[calc(100vw-2rem)] rounded-structural border border-border bg-surface p-3 text-sm shadow-md"
        >
          <SharePanel
            resource={resource}
            recordId={recordId}
            label={model.label}
            permissions={model.share_permissions ?? []}
            headingId={headingId}
          />
        </div>
      )}
    </div>
  );
}
