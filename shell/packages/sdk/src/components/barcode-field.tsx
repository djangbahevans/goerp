import * as DialogPrimitive from "@radix-ui/react-dialog";
import { BarcodeDetector, type BarcodeFormat } from "barcode-detector/pure";
import { CameraOff, ScanBarcode } from "lucide-react";
import type { ReactNode } from "react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { fieldInputClassName } from "./field-input-styles.js";
import { MODAL_OVERLAY_CLASSES } from "./modal-overlay.js";
import { Spinner } from "./spinner.js";

export type { BarcodeFormat };

// barcode-field.md's own proposed default, stated explicitly since no
// default is specified anywhere in the source material.
const DEFAULT_FORMATS: BarcodeFormat[] = ["qr_code", "ean_13", "ean_8", "upc_a", "upc_e", "code_128", "code_39"];

// A live video frame needs repeated detect() attempts, not one call.
const POLL_INTERVAL_MS = 300;
// Consecutive decode failures (e.g. a WASM module that can never load)
// before giving up rather than polling forever with nothing to show for it.
const FAILURE_THRESHOLD = 10;

export interface BarcodeFieldProps {
  id?: string | undefined;
  value: string;
  onChange: (value: string) => void;
  // Fires in addition to onChange, only on a decode — lets a caller tell a scan apart from manual typing.
  onScan?: ((value: string) => void) | undefined;
  formats?: BarcodeFormat[] | undefined;
  placeholder?: string | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
  // Drives the pending-lookup spinner for the on_scan_route wrapper (which this component has no knowledge of).
  isLookingUp?: boolean | undefined;
}

type ScanState = "idle" | "scanning" | "denied";

// The visible denied-state message differs from its aria-live announcement
// (barcode-field.md's Accessibility section states both explicitly) and
// differs again per failure reason, so this table is the single source for
// the former while `announcement` (state, below) covers the latter.
const DENIED_MESSAGES = {
  permission: "Camera access is needed to scan — you can still type the code below.",
  preview: "The camera preview couldn't start — you can still type the code below.",
  decode: "Scanning isn't working right now — you can still type the code below.",
} as const;
type DeniedReason = keyof typeof DENIED_MESSAGES;

const CONTENT_CLASSES =
  "fixed inset-0 z-(--z-modal) flex items-center justify-center p-0 sm:p-4 focus:outline-none data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in] sm:data-[state=open]:animate-[alert-dialog-content-show_var(--duration-slow)_ease-out] sm:data-[state=closed]:animate-[alert-dialog-content-hide_var(--duration-slow)_ease-in] motion-reduce:data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] motion-reduce:data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in]";

const OVERLAY_BUTTON_CLASS_NAME =
  "flex h-11 min-w-11 items-center justify-center rounded-control bg-surface px-4 text-sm text-text shadow-lg hover:opacity-90 focus-visible:outline-none focus-visible:shadow-focus";

const TRAILING_BUTTON_STYLE = {
  position: "absolute",
  insetInlineEnd: "var(--space-2)",
  top: "50%",
  transform: "translateY(-50%)",
} as const;
const LEADING_ICON_STYLE = {
  position: "absolute",
  insetInlineStart: "var(--space-2)",
  top: "50%",
  transform: "translateY(-50%)",
} as const;

interface ScanSession {
  stream: MediaStream;
  pollId: number;
}

export function BarcodeField({
  id,
  value,
  onChange,
  onScan,
  formats = DEFAULT_FORMATS,
  placeholder = "Scan or enter code",
  error,
  disabled = false,
  isLookingUp = false,
}: BarcodeFieldProps): ReactNode {
  const [scanState, setScanState] = useState<ScanState>("idle");
  const [deniedReason, setDeniedReason] = useState<DeniedReason>("permission");
  const [announcement, setAnnouncement] = useState("");
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const sessionRef = useRef<ScanSession | null>(null);
  const detectingRef = useRef(false);
  const manualInputRef = useRef<HTMLInputElement | null>(null);
  const scanTriggerRef = useRef<HTMLButtonElement | null>(null);
  // Focus target once the overlay closes — the manual input only when closing out of the denied message.
  const closeFocusTargetRef = useRef<"trigger" | "manual">("trigger");
  // Invalidates an in-flight startScan() attempt cancelled before it resolves.
  const scanIdRef = useRef(0);

  // Falls back to the default set if `formats` is invalid — the real constructor throws synchronously otherwise.
  const detector = useMemo(() => {
    try {
      return new BarcodeDetector({ formats });
    } catch (e) {
      console.warn("BarcodeField: invalid `formats`, falling back to the default set", e);
      return new BarcodeDetector({ formats: DEFAULT_FORMATS });
    }
  }, [formats]);

  const stopStream = useCallback(() => {
    scanIdRef.current++;
    if (sessionRef.current) {
      window.clearInterval(sessionRef.current.pollId);
      for (const track of sessionRef.current.stream.getTracks()) track.stop();
      sessionRef.current = null;
    }
  }, []);

  // Never leave a camera stream running behind a closed or unmounted field.
  useEffect(() => stopStream, [stopStream]);

  const closeOverlay = useCallback(
    (focusTarget: "trigger" | "manual") => {
      stopStream();
      closeFocusTargetRef.current = focusTarget;
      setScanState("idle");
    },
    [stopStream],
  );

  const startScan = useCallback(async () => {
    if (scanState !== "idle") return;
    const myScanId = ++scanIdRef.current;
    setScanState("scanning");

    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
    } catch {
      if (myScanId === scanIdRef.current) {
        setDeniedReason("permission");
        setScanState("denied");
        setAnnouncement("Camera access denied — you can type the code instead");
      }
      return;
    }
    // Cancelled (or unmounted) while the permission prompt was pending.
    if (myScanId !== scanIdRef.current) {
      for (const track of stream.getTracks()) track.stop();
      return;
    }

    if (videoRef.current) {
      videoRef.current.srcObject = stream;
      try {
        await videoRef.current.play();
      } catch {
        // Access was granted but the preview failed — not a permission denial, but still needs releasing.
        for (const track of stream.getTracks()) track.stop();
        if (myScanId === scanIdRef.current) {
          setDeniedReason("preview");
          setScanState("denied");
          setAnnouncement("Camera preview failed to start — you can type the code instead");
        }
        return;
      }
    }
    if (myScanId !== scanIdRef.current) {
      for (const track of stream.getTracks()) track.stop();
      return;
    }

    let consecutiveFailures = 0;
    const pollId = window.setInterval(async () => {
      if (detectingRef.current || !videoRef.current) return;
      detectingRef.current = true;
      try {
        const [match] = await detector.detect(videoRef.current);
        consecutiveFailures = 0;
        if (myScanId === scanIdRef.current && match?.rawValue) {
          onChange(match.rawValue);
          onScan?.(match.rawValue);
          setAnnouncement("Barcode scanned");
          closeOverlay("trigger");
        }
      } catch {
        consecutiveFailures++;
        if (consecutiveFailures >= FAILURE_THRESHOLD && myScanId === scanIdRef.current) {
          stopStream();
          setDeniedReason("decode");
          setScanState("denied");
          setAnnouncement("Scanning isn't working right now — you can type the code instead");
        }
      } finally {
        detectingRef.current = false;
      }
    }, POLL_INTERVAL_MS);
    sessionRef.current = { stream, pollId };
  }, [scanState, detector, onChange, onScan, closeOverlay, stopStream]);

  return (
    <div className="flex flex-col gap-1">
      <div className="relative">
        <input
          ref={manualInputRef}
          id={id}
          type="text"
          value={value}
          placeholder={placeholder}
          disabled={disabled || isLookingUp}
          aria-invalid={error !== undefined}
          onChange={(e) => onChange(e.target.value)}
          className={`w-full ${fieldInputClassName(error !== undefined, "input", "sans")} ${
            isLookingUp ? "ps-8" : ""
          } pe-10`}
        />
        {isLookingUp && (
          <span aria-hidden="true" style={LEADING_ICON_STYLE} className="text-text-secondary">
            <Spinner size={16} />
          </span>
        )}
        <button
          ref={scanTriggerRef}
          type="button"
          aria-label="Scan barcode"
          disabled={disabled || isLookingUp || scanState !== "idle"}
          onClick={() => {
            closeFocusTargetRef.current = "trigger";
            void startScan();
          }}
          style={TRAILING_BUTTON_STYLE}
          className="flex h-11 w-11 items-center justify-center rounded-control text-text-secondary hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
        >
          <ScanBarcode size={16} />
        </button>
      </div>
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
      <span aria-live="polite" className="sr-only">
        {announcement}
      </span>

      <DialogPrimitive.Root
        open={scanState !== "idle"}
        onOpenChange={(next) => {
          if (!next) closeOverlay(closeFocusTargetRef.current);
        }}
      >
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
          <DialogPrimitive.Content
            aria-label="Scan barcode"
            className={CONTENT_CLASSES}
            onEscapeKeyDown={() => closeOverlay("trigger")}
            onPointerDownOutside={(event) => event.preventDefault()}
            onCloseAutoFocus={(event) => {
              event.preventDefault();
              (closeFocusTargetRef.current === "manual" ? manualInputRef : scanTriggerRef).current?.focus();
            }}
          >
            <div className="flex h-full w-full flex-col bg-surface sm:h-auto sm:w-auto sm:max-w-[min(480px,calc(100vw-32px))] sm:rounded-structural sm:shadow-lg">
              {scanState === "scanning" ? (
                <div className="relative flex flex-1 items-center justify-center overflow-hidden bg-black sm:aspect-4/3 sm:flex-none">
                  <video
                    ref={videoRef}
                    aria-hidden="true"
                    tabIndex={-1}
                    muted
                    playsInline
                    className="h-full w-full object-cover"
                  />
                  <span
                    aria-hidden="true"
                    className="pointer-events-none absolute inset-6 rounded-control border-2 border-primary filter-[drop-shadow(0_0_4px_rgb(0_0_0/0.6))]"
                  />
                  <button
                    type="button"
                    onClick={() => closeOverlay("trigger")}
                    className={`absolute bottom-6 ${OVERLAY_BUTTON_CLASS_NAME}`}
                  >
                    Cancel
                  </button>
                </div>
              ) : (
                <div className="flex flex-1 flex-col items-center justify-center gap-4 p-6 text-center sm:flex-none">
                  <CameraOff aria-hidden="true" size={32} className="text-text-secondary" />
                  <p className="text-sm text-text">{DENIED_MESSAGES[deniedReason]}</p>
                  <button type="button" onClick={() => closeOverlay("manual")} className={OVERLAY_BUTTON_CLASS_NAME}>
                    Close
                  </button>
                </div>
              )}
            </div>
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
    </div>
  );
}
