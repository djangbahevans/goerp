import type { ReactNode, PointerEvent as ReactPointerEvent } from "react";
import { useEffect, useRef, useState } from "react";
import { Button } from "./button.js";
import { FieldWrapper } from "./field-wrapper.js";
import type { UploadHandle } from "./file-field-upload.js";
import { uploadFile } from "./file-field-upload.js";
import { SegmentedField } from "./segmented-field.js";
import { TextInput } from "./text-input.js";

type UploadFn = (
  file: File | Blob,
  filename: string,
  purpose: string,
  onProgress: (percent: number) => void,
) => UploadHandle;

export interface SignaturePadProps {
  // A file id from POST /storage/upload, or a data URL.
  value: string | null;
  onChange: (value: string | null) => void;
  disabled?: boolean | undefined;
  // Pre-fills the typed-signature input, usually the signed-in user's name.
  signerName?: string | undefined;
  // Signed URL for a `value` file id that wasn't captured in this session.
  valueUrl?: string | undefined;
  // Names the whole control; the canvas and image carry their own state names.
  ariaLabel?: string | undefined;
  // Injectable for Storybook, which has no /storage/upload backend.
  uploadFn?: UploadFn | undefined;
}

type Mode = "draw" | "type";

const MODE_OPTIONS = [
  { value: "draw", label: "Draw" },
  { value: "type", label: "Type" },
];

const CANVAS_WIDTH = 200;
const CANVAS_HEIGHT = 80;
const CANVAS_PADDING = 8;
const TYPED_FONT_SIZE = 40;
const TYPED_FONT_FAMILY = "Caveat";
const MAX_NAME_LENGTH = 80;

function textColor(canvas: HTMLCanvasElement): string {
  return getComputedStyle(canvas).getPropertyValue("--color-text");
}

function drawTypedName(canvas: HTMLCanvasElement, name: string): void {
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  if (name === "") return;
  ctx.fillStyle = textColor(canvas);
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.font = `${TYPED_FONT_SIZE}px ${TYPED_FONT_FAMILY}`;
  const fit = Math.min(1, (canvas.width - CANVAS_PADDING * 2) / ctx.measureText(name).width);
  ctx.font = `${TYPED_FONT_SIZE * fit}px ${TYPED_FONT_FAMILY}`;
  ctx.fillText(name, canvas.width / 2, canvas.height / 2);
}

// Caveat is split into unicode-range subsets; loading with the text itself
// fetches the subsets that text needs, so the canvas never falls back to
// another font for part of a name.
async function loadSignatureFont(text: string): Promise<void> {
  await import("@fontsource/caveat/400.css");
  const faces = await document.fonts.load(`${TYPED_FONT_SIZE}px ${TYPED_FONT_FAMILY}`, text === "" ? " " : text);
  if (faces.length === 0) throw new Error("signature font unavailable");
}

// Captures a drawn or typed signature as a PNG, uploads it, and reports the
// file id. Draw mode captures when a stroke ends; Type mode on "Adopt signature".
export function SignaturePad({
  value,
  onChange,
  disabled = false,
  signerName,
  valueUrl,
  ariaLabel,
  uploadFn = uploadFile,
}: SignaturePadProps): ReactNode {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const drawing = useRef(false);
  const stroked = useRef(false);
  const alive = useRef(true);
  const uploadRef = useRef<UploadHandle | null>(null);
  const [preview, setPreview] = useState<{ fileId: string; url: string } | null>(null);
  const [mode, setMode] = useState<Mode>("draw");
  const [typed, setTyped] = useState<string | null>(null);
  const [fontState, setFontState] = useState<"idle" | "ready" | "failed">("idle");
  const [phase, setPhase] = useState<"idle" | "uploading" | "failed">("idle");
  const [announceCaptured, setAnnounceCaptured] = useState(false);

  const name = typed ?? signerName ?? "";
  const typedName = name.trim();
  const busy = disabled || phase === "uploading";

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      uploadRef.current?.abort();
    };
  }, []);

  useEffect(() => {
    if (preview) return () => URL.revokeObjectURL(preview.url);
  }, [preview]);

  useEffect(() => {
    if (mode !== "type" || fontState !== "idle") return;
    let cancelled = false;
    loadSignatureFont(typedName).then(
      () => !cancelled && setFontState("ready"),
      () => !cancelled && setFontState("failed"),
    );
    return () => {
      cancelled = true;
    };
  }, [mode, fontState, typedName]);

  const showTypedPreview = mode === "type" && fontState === "ready" && !value;
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!showTypedPreview || !canvas) return;
    let cancelled = false;
    loadSignatureFont(typedName).then(
      () => !cancelled && drawTypedName(canvas, typedName),
      () => !cancelled && setFontState("failed"),
    );
    return () => {
      cancelled = true;
    };
  }, [showTypedPreview, typedName]);

  const clearCanvas = () => {
    const canvas = canvasRef.current;
    canvas?.getContext("2d")?.clearRect(0, 0, canvas.width, canvas.height);
  };

  const pos = (e: ReactPointerEvent<HTMLCanvasElement>, canvas: HTMLCanvasElement) => {
    const rect = canvas.getBoundingClientRect();
    return { x: e.clientX - rect.left, y: e.clientY - rect.top };
  };

  const start = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    if (busy || mode !== "draw") return;
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) return;
    drawing.current = true;
    stroked.current = false;
    ctx.strokeStyle = textColor(canvas);
    const { x, y } = pos(e, canvas);
    ctx.beginPath();
    ctx.moveTo(x, y);
  };

  const move = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!drawing.current || !canvas || !ctx) return;
    const { x, y } = pos(e, canvas);
    stroked.current = true;
    ctx.lineTo(x, y);
    ctx.stroke();
  };

  const upload = (canvas: HTMLCanvasElement) => {
    canvas.toBlob((blob) => {
      if (!alive.current) return;
      if (!blob) {
        setPhase("failed");
        return;
      }
      const handle = uploadFn(blob, "signature.png", "attachments", () => {});
      uploadRef.current = handle;
      handle.promise.then(
        (result) => {
          uploadRef.current = null;
          setPreview({ fileId: result.fileId, url: URL.createObjectURL(blob) });
          setPhase("idle");
          setAnnounceCaptured(true);
          onChange(result.fileId);
        },
        () => {
          uploadRef.current = null;
          setPhase("failed");
        },
      );
    }, "image/png");
  };

  const capture = () => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    setPhase("uploading");
    if (mode === "draw") {
      upload(canvas);
      return;
    }
    loadSignatureFont(typedName).then(
      () => {
        if (!alive.current) return;
        drawTypedName(canvas, typedName);
        upload(canvas);
      },
      () => alive.current && setPhase("failed"),
    );
  };

  const end = () => {
    if (!drawing.current) return;
    drawing.current = false;
    if (stroked.current) capture();
  };

  const clear = () => {
    clearCanvas();
    setPhase("idle");
    setAnnounceCaptured(false);
    onChange(null);
  };

  const switchMode = (next: string) => {
    if (next === mode) return;
    clearCanvas();
    setTyped(null);
    setPhase("idle");
    setMode(next as Mode);
  };

  const capturedSrc = value?.startsWith("data:") ? value : preview?.fileId === value ? preview.url : valueUrl;

  return (
    <fieldset aria-label={ariaLabel ?? "Signature"} className="flex min-w-0 flex-col items-start gap-2">
      {value ? (
        capturedSrc ? (
          <img src={capturedSrc} alt="Signature captured" className="max-w-50" />
        ) : (
          <span className="text-sm text-text-secondary">Signature captured</span>
        )
      ) : (
        <>
          <SegmentedField size="sm" options={MODE_OPTIONS} value={mode} onChange={switchMode} disabled={busy} />
          {mode === "type" && (
            <FieldWrapper label="Type your full name">
              <TextInput
                value={name}
                onChange={setTyped}
                maxLength={MAX_NAME_LENGTH}
                disabled={busy}
                autoComplete="name"
              />
            </FieldWrapper>
          )}
          <canvas
            ref={canvasRef}
            role="img"
            aria-label={mode === "draw" ? "Signature drawing area" : "Signature preview"}
            width={CANVAS_WIDTH}
            height={CANVAS_HEIGHT}
            className="touch-none rounded-control border border-border-control"
            onPointerDown={start}
            onPointerMove={move}
            onPointerUp={end}
            onPointerLeave={end}
          />
        </>
      )}
      <div className="flex items-center gap-2">
        {mode === "type" && !value ? (
          <Button
            variant="secondary"
            size="sm"
            disabled={busy || typedName === "" || fontState !== "ready"}
            onClick={capture}
          >
            Adopt signature
          </Button>
        ) : (
          <Button variant="ghost" size="sm" disabled={busy} onClick={clear}>
            Clear
          </Button>
        )}
        {phase === "failed" && (
          <>
            <span role="alert" className="text-danger text-sm">
              Couldn't save the signature.
            </span>
            <Button variant="ghost" size="sm" disabled={disabled} onClick={capture}>
              Retry
            </Button>
          </>
        )}
        {fontState === "failed" && mode === "type" && (
          <span role="alert" className="text-danger text-sm">
            The signature font failed to load. Switch to Draw.
          </span>
        )}
      </div>
      <span role="status" className="sr-only">
        {announceCaptured && value ? "Signature captured." : ""}
      </span>
    </fieldset>
  );
}
