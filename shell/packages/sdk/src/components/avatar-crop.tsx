import type { KeyboardEvent, ReactNode, PointerEvent as ReactPointerEvent } from "react";
import { useEffect, useRef, useState } from "react";
import { actionButtonClassName } from "./action-button-styles.js";

export interface AvatarCropProps {
  file: File;
  onApply: (blob: Blob) => void;
  onCancel: () => void;
  disabled?: boolean | undefined;
}

// On-screen preview size, a stated pixel value like SignaturePad's own
// 200×80 canvas — the exported crop is always 512×512 (file-field.md),
// independent of this.
const VIEWPORT_SIZE = 200;
const EXPORT_SIZE = 512;
const ZOOM_MIN = 1;
const ZOOM_MAX = 4;
const ZOOM_STEP = 0.1;
const NUDGE_STEP = 10;

interface Offset {
  x: number;
  y: number;
}

function clamp(n: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, n));
}

// Offsets are always tracked in VIEWPORT_SIZE-space; drawing at a larger
// target size (the 512px export) just scales the same relative framing up.
function maxOffsetFor(img: HTMLImageElement, zoom: number, targetSize: number): Offset {
  const baseScale = targetSize / Math.min(img.naturalWidth, img.naturalHeight);
  const scale = baseScale * zoom;
  const dispW = img.naturalWidth * scale;
  const dispH = img.naturalHeight * scale;
  return { x: Math.max(0, (dispW - targetSize) / 2), y: Math.max(0, (dispH - targetSize) / 2) };
}

function drawCrop(
  ctx: CanvasRenderingContext2D,
  img: HTMLImageElement,
  targetSize: number,
  zoom: number,
  offset: Offset,
): void {
  const baseScale = targetSize / Math.min(img.naturalWidth, img.naturalHeight);
  const scale = baseScale * zoom;
  const dispW = img.naturalWidth * scale;
  const dispH = img.naturalHeight * scale;
  const scaleFactor = targetSize / VIEWPORT_SIZE;
  ctx.clearRect(0, 0, targetSize, targetSize);
  ctx.save();
  ctx.beginPath();
  ctx.arc(targetSize / 2, targetSize / 2, targetSize / 2, 0, Math.PI * 2);
  ctx.clip();
  ctx.drawImage(
    img,
    targetSize / 2 - dispW / 2 + offset.x * scaleFactor,
    targetSize / 2 - dispH / 2 + offset.y * scaleFactor,
    dispW,
    dispH,
  );
  ctx.restore();
}

export function AvatarCrop({ file, onApply, onCancel, disabled = false }: AvatarCropProps): ReactNode {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const imgRef = useRef<HTMLImageElement | null>(null);
  const dragRef = useRef<{ startPointer: Offset; startOffset: Offset } | null>(null);
  const [zoom, setZoom] = useState(1);
  const [offset, setOffset] = useState<Offset>({ x: 0, y: 0 });
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      imgRef.current = img;
      setReady(true);
    };
    img.src = url;
    return () => {
      URL.revokeObjectURL(url);
      imgRef.current = null;
    };
  }, [file]);

  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    const img = imgRef.current;
    if (!canvas || !ctx || !img || !ready) return;
    drawCrop(ctx, img, VIEWPORT_SIZE, zoom, offset);
  }, [zoom, offset, ready]);

  function moveTo(next: Offset, nextZoom = zoom): void {
    const img = imgRef.current;
    if (!img) return;
    const max = maxOffsetFor(img, nextZoom, VIEWPORT_SIZE);
    setOffset({ x: clamp(next.x, -max.x, max.x), y: clamp(next.y, -max.y, max.y) });
  }

  function changeZoom(next: number): void {
    const clamped = clamp(next, ZOOM_MIN, ZOOM_MAX);
    setZoom(clamped);
    moveTo(offset, clamped);
  }

  function handlePointerDown(event: ReactPointerEvent<HTMLCanvasElement>): void {
    if (disabled) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = { startPointer: { x: event.clientX, y: event.clientY }, startOffset: offset };
  }

  function handlePointerMove(event: ReactPointerEvent<HTMLCanvasElement>): void {
    const drag = dragRef.current;
    if (!drag) return;
    moveTo({
      x: drag.startOffset.x + (event.clientX - drag.startPointer.x),
      y: drag.startOffset.y + (event.clientY - drag.startPointer.y),
    });
  }

  function handlePointerUp(): void {
    dragRef.current = null;
  }

  function handleKeyDown(event: KeyboardEvent<HTMLCanvasElement>): void {
    if (disabled) return;
    switch (event.key) {
      case "ArrowUp":
        event.preventDefault();
        moveTo({ x: offset.x, y: offset.y + NUDGE_STEP });
        break;
      case "ArrowDown":
        event.preventDefault();
        moveTo({ x: offset.x, y: offset.y - NUDGE_STEP });
        break;
      case "ArrowLeft":
        event.preventDefault();
        moveTo({ x: offset.x + NUDGE_STEP, y: offset.y });
        break;
      case "ArrowRight":
        event.preventDefault();
        moveTo({ x: offset.x - NUDGE_STEP, y: offset.y });
        break;
      case "+":
      case "=":
        event.preventDefault();
        changeZoom(zoom + ZOOM_STEP);
        break;
      case "-":
        event.preventDefault();
        changeZoom(zoom - ZOOM_STEP);
        break;
      default:
        break;
    }
  }

  function apply(): void {
    const img = imgRef.current;
    if (!img) return;
    const exportCanvas = document.createElement("canvas");
    exportCanvas.width = EXPORT_SIZE;
    exportCanvas.height = EXPORT_SIZE;
    const ctx = exportCanvas.getContext("2d");
    if (!ctx) return;
    drawCrop(ctx, img, EXPORT_SIZE, zoom, offset);
    exportCanvas.toBlob((blob) => {
      if (blob) onApply(blob);
    }, "image/png");
  }

  return (
    <div className="flex flex-col items-center gap-3">
      <canvas
        ref={canvasRef}
        width={VIEWPORT_SIZE}
        height={VIEWPORT_SIZE}
        tabIndex={0}
        aria-label="Crop avatar image. Drag to reposition. Arrow keys move, plus and minus zoom."
        className="cursor-move touch-none rounded-full border border-border shadow-md focus-visible:outline-none focus-visible:shadow-focus"
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerLeave={handlePointerUp}
        onKeyDown={handleKeyDown}
      />
      <input
        type="range"
        aria-label="Zoom"
        min={ZOOM_MIN}
        max={ZOOM_MAX}
        step={ZOOM_STEP}
        value={zoom}
        disabled={disabled}
        onChange={(event) => changeZoom(Number(event.target.value))}
        style={{ width: VIEWPORT_SIZE }}
      />
      <span className="flex gap-2">
        <button
          type="button"
          disabled={disabled}
          data-disabled={disabled ? "true" : undefined}
          onClick={onCancel}
          className={actionButtonClassName("secondary", "sm")}
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={disabled || !ready}
          data-disabled={disabled || !ready ? "true" : undefined}
          onClick={apply}
          className={actionButtonClassName("primary", "sm")}
        >
          Apply
        </button>
      </span>
    </div>
  );
}
