import type { ReactNode, PointerEvent as ReactPointerEvent } from "react";
import { useRef } from "react";

export interface SignaturePadProps {
  value: string | null;
  onChange: (value: string | null) => void;
  disabled?: boolean | undefined;
}

// Pointer-driven canvas capture, no pressure-sensitivity or undo. Renders
// the captured PNG data URL once drawn; "Clear" resets to a blank canvas.
export function SignaturePad({ value, onChange, disabled = false }: SignaturePadProps): ReactNode {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const drawing = useRef(false);

  const pos = (e: ReactPointerEvent<HTMLCanvasElement>, canvas: HTMLCanvasElement) => {
    const rect = canvas.getBoundingClientRect();
    return { x: e.clientX - rect.left, y: e.clientY - rect.top };
  };

  const start = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    if (disabled) return;
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) return;
    drawing.current = true;
    const { x, y } = pos(e, canvas);
    ctx.beginPath();
    ctx.moveTo(x, y);
  };

  const move = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!drawing.current || !canvas || !ctx) return;
    const { x, y } = pos(e, canvas);
    ctx.lineTo(x, y);
    ctx.stroke();
  };

  const end = () => {
    if (!drawing.current) return;
    drawing.current = false;
    const canvas = canvasRef.current;
    if (canvas) onChange(canvas.toDataURL("image/png"));
  };

  const clear = () => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (canvas && ctx) ctx.clearRect(0, 0, canvas.width, canvas.height);
    onChange(null);
  };

  return (
    <span>
      {value ? (
        <img src={value} alt="Signature" style={{ maxWidth: 200 }} />
      ) : (
        <canvas
          ref={canvasRef}
          width={200}
          height={80}
          style={{ border: "1px solid", touchAction: "none" }}
          onPointerDown={start}
          onPointerMove={move}
          onPointerUp={end}
          onPointerLeave={end}
        />
      )}
      <button type="button" disabled={disabled} onClick={clear}>
        Clear
      </button>
    </span>
  );
}
