import type { CSSProperties, ReactNode } from "react";
import { useEffect, useId } from "react";

export interface SliderFieldProps {
  id?: string | undefined;
  value: number;
  min: number;
  max: number;
  step: number;
  onChange: (value: number) => void;
  disabled?: boolean | undefined;
}

// Vendor pseudo-elements (::-webkit-slider-thumb, ...) have no Tailwind
// utility or CSS-asset pipeline to reach through in this tsc-only package —
// docs/components/slider-field.md's "Build constraint" has the full reasoning.
const SLIDER_STYLE = `
.slider-field-input {
  -webkit-appearance: none;
  appearance: none;
  background: transparent;
  cursor: pointer;
  --slider-fill-color: var(--color-primary);
}
.slider-field-input:hover:not(:disabled) {
  --slider-fill-color: var(--color-primary-hover);
}
.slider-field-input:disabled {
  cursor: not-allowed;
}
.slider-field-input:focus-visible {
  outline: none;
}
.slider-field-input::-webkit-slider-runnable-track {
  height: var(--space-2);
  border-radius: var(--radius-full);
  background: linear-gradient(
    to right,
    var(--slider-fill-color) 0%,
    var(--slider-fill-color) var(--slider-fill),
    var(--color-border) var(--slider-fill),
    var(--color-border) 100%
  );
}
.slider-field-input::-webkit-slider-thumb {
  -webkit-appearance: none;
  height: var(--space-4);
  width: var(--space-4);
  margin-top: calc((var(--space-2) - var(--space-4)) / 2);
  border-radius: var(--radius-full);
  background: var(--color-surface);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
}
.slider-field-input:focus-visible::-webkit-slider-thumb {
  box-shadow: var(--shadow-focus);
}
.slider-field-input:disabled::-webkit-slider-thumb {
  cursor: not-allowed;
}
.slider-field-input::-moz-range-track {
  height: var(--space-2);
  border-radius: var(--radius-full);
  background: var(--color-border);
}
.slider-field-input::-moz-range-progress {
  height: var(--space-2);
  border-radius: var(--radius-full);
  background: var(--slider-fill-color);
}
.slider-field-input::-moz-range-thumb {
  height: var(--space-4);
  width: var(--space-4);
  border: none;
  border-radius: var(--radius-full);
  background: var(--color-surface);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
}
.slider-field-input:focus-visible::-moz-range-thumb {
  box-shadow: var(--shadow-focus);
}
.slider-field-input:disabled::-moz-range-thumb {
  cursor: not-allowed;
}
`;

// Injected once into <head> on first mount rather than per-instance — a form
// with several sliders would otherwise render one identical <style> tag each.
let sliderStyleInjected = false;
function ensureSliderStyleInjected(): void {
  if (sliderStyleInjected) return;
  const style = document.createElement("style");
  style.textContent = SLIDER_STYLE;
  document.head.appendChild(style);
  sliderStyleInjected = true;
}

export function SliderField({
  id: idProp,
  value,
  min,
  max,
  step,
  onChange,
  disabled = false,
}: SliderFieldProps): ReactNode {
  useEffect(ensureSliderStyleInjected, []);
  const generatedId = useId();
  const id = idProp ?? generatedId;
  const percent = max > min ? ((value - min) / (max - min)) * 100 : 0;
  return (
    <div className="flex flex-col gap-1">
      <output htmlFor={id} className="self-end text-sm text-text-secondary">
        {value}
      </output>
      <input
        id={id}
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(Number(e.target.value))}
        className="slider-field-input w-full disabled:cursor-not-allowed disabled:opacity-50"
        style={{ "--slider-fill": `${percent}%` } as CSSProperties}
      />
    </div>
  );
}
