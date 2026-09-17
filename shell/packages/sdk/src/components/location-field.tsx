import { layers as basemapLayers, DARK, LIGHT } from "@protomaps/basemaps";
// maplibre-gl ships only named exports (no default) — the consuming app is
// responsible for importing "maplibre-gl/dist/maplibre-gl.css" itself
// (main.tsx, alongside its other global styles), the same way this package
// never bundles CSS for its own consumers to pick up automatically.
import * as maplibregl from "maplibre-gl";
import { Protocol } from "pmtiles";
import type { ReactNode } from "react";
import { useEffect, useId, useRef } from "react";
import { useTheme } from "../react/use-theme.js";
import { fieldInputClassName } from "./field-input-styles.js";

export interface LocationValue {
  lat: number;
  lng: number;
}

export interface LocationFieldProps {
  label?: string | undefined;
  // External label target — the map canvas isn't natively labelable (goerp#698).
  ariaLabelledBy?: string | undefined;
  // Partial, not optional: a value can have only one of lat/lng mid-edit.
  value?: Partial<LocationValue> | undefined;
  onChange: (value: Partial<LocationValue>) => void;
  id?: string | undefined;
  disabled?: boolean | undefined;
  error?: string | undefined;
  // Resolved PMTiles archive URL. Undefined renders a plain background.
  tileUrl?: string | undefined;
}

// Accra, Ghana — this project's own default locale, not (0, 0).
const EMPTY_CENTER: maplibregl.LngLatLike = [-0.187, 5.6037];
const EMPTY_ZOOM = 6;
const PMTILES_SOURCE_ID = "protomaps";
const MARKER_HIT_SIZE_PX = 44; // WCAG 2.5.5 minimum, regardless of the visual pin's own size.

let pmtilesProtocolRegistered = false;
function ensurePmtilesProtocolRegistered(): void {
  if (pmtilesProtocolRegistered) return;
  const protocol = new Protocol();
  maplibregl.addProtocol("pmtiles", protocol.tile);
  pmtilesProtocolRegistered = true;
}

function isCompleteValue(value: Partial<LocationValue> | undefined): value is LocationValue {
  return value !== undefined && Number.isFinite(value.lat) && Number.isFinite(value.lng);
}

function clampLat(n: number): number {
  return Math.min(90, Math.max(-90, n));
}

function clampLng(n: number): number {
  return Math.min(180, Math.max(-180, n));
}

// No self-hosted glyph/sprite server exists yet — omitting glyphs/sprite
// just skips label/icon layers; roads/water/buildings still render fine.
function buildStyle(tileUrl: string | undefined, theme: "light" | "dark"): maplibregl.StyleSpecification {
  if (tileUrl === undefined) {
    return {
      version: 8,
      sources: {},
      layers: [
        {
          id: "background",
          type: "background",
          paint: { "background-color": theme === "dark" ? "#2B303A" : "#F5F6F8" },
        },
      ],
    };
  }
  return {
    version: 8,
    sources: { [PMTILES_SOURCE_ID]: { type: "vector", url: `pmtiles://${tileUrl}` } },
    layers: basemapLayers(PMTILES_SOURCE_ID, theme === "dark" ? DARK : LIGHT, { lang: "en" }),
  };
}

function createMarkerElement(): HTMLDivElement {
  const el = document.createElement("div");
  el.style.width = `${MARKER_HIT_SIZE_PX}px`;
  el.style.height = `${MARKER_HIT_SIZE_PX}px`;
  el.style.display = "flex";
  el.style.alignItems = "center";
  el.style.justifyContent = "center";
  el.style.cursor = "grab";
  const dot = document.createElement("div");
  dot.style.width = "16px";
  dot.style.height = "16px";
  dot.style.borderRadius = "9999px";
  dot.style.background = "var(--color-primary)";
  dot.style.border = "2px solid white";
  dot.style.boxShadow = "var(--shadow-sm)";
  el.appendChild(dot);
  return el;
}

export function LocationField({
  label,
  ariaLabelledBy,
  value,
  onChange,
  id: idProp,
  disabled = false,
  error,
  tileUrl,
}: LocationFieldProps): ReactNode {
  const generatedId = useId();
  const id = idProp ?? generatedId;
  const labelledById = ariaLabelledBy ?? (label !== undefined ? id : undefined);
  const { theme } = useTheme();

  const mapHostRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<maplibregl.Map | null>(null);
  const markerRef = useRef<maplibregl.Marker | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const disabledRef = useRef(disabled);
  disabledRef.current = disabled;

  // Mounted once; later prop changes are applied imperatively below instead
  // of recreating the map (which would reset the user's pan/zoom).
  // biome-ignore lint/correctness/useExhaustiveDependencies: intentionally mount-once.
  useEffect(() => {
    const host = mapHostRef.current;
    if (!host) return;
    ensurePmtilesProtocolRegistered();

    let map: maplibregl.Map;
    try {
      map = new maplibregl.Map({
        container: host,
        style: buildStyle(tileUrl, theme),
        center: isCompleteValue(value) ? [value.lng, value.lat] : EMPTY_CENTER,
        zoom: EMPTY_ZOOM,
        attributionControl: false,
      });
    } catch {
      // No WebGL support — the coordinate inputs below remain fully usable.
      return;
    }
    mapRef.current = map;

    map.on("click", (e: maplibregl.MapMouseEvent) => {
      if (disabledRef.current) return;
      const wrapped = e.lngLat.wrap();
      onChangeRef.current({ lat: clampLat(wrapped.lat), lng: clampLng(wrapped.lng) });
    });

    if (disabled) {
      map.scrollZoom.disable();
      map.dragPan.disable();
      map.doubleClickZoom.disable();
      map.touchZoomRotate.disable();
      map.keyboard.disable();
    }
    // Excluded from both the tab order and a11y tree (WCAG 4.1.2) — the
    // coordinate inputs are the real accessible interface, not this canvas.
    map.getCanvas().setAttribute("tabindex", "-1");
    map.getCanvas().setAttribute("aria-hidden", "true");

    // Marker creation is left to the [value] effect below, which also runs
    // after this mount.
    return () => {
      markerRef.current?.remove();
      markerRef.current = null;
      map.remove();
      mapRef.current = null;
    };
  }, []);

  // Disabled: full lock, no partial interactivity left available (matches
  // CodeField/ColorPicker's shared disabled treatment).
  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    for (const handler of [map.scrollZoom, map.dragPan, map.doubleClickZoom, map.touchZoomRotate, map.keyboard]) {
      if (disabled) handler.disable();
      else handler.enable();
    }
    markerRef.current?.setDraggable(!disabled);
  }, [disabled]);

  useEffect(() => {
    mapRef.current?.setStyle(buildStyle(tileUrl, theme));
  }, [tileUrl, theme]);

  // Creates, repositions, or removes the marker for any way `value` changes
  // (click, drag, manual entry, or an external change).
  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    if (!isCompleteValue(value)) {
      markerRef.current?.remove();
      markerRef.current = null;
      return;
    }
    const lngLat: maplibregl.LngLatLike = [value.lng, value.lat];
    if (markerRef.current) {
      markerRef.current.setLngLat(lngLat);
    } else {
      const marker = new maplibregl.Marker({ element: createMarkerElement(), draggable: !disabledRef.current })
        .setLngLat(lngLat)
        .addTo(map);
      marker.on("dragend", () => {
        const dragged = marker.getLngLat().wrap();
        onChangeRef.current({ lat: clampLat(dragged.lat), lng: clampLng(dragged.lng) });
      });
      markerRef.current = marker;
    }
    map.panTo(lngLat);
  }, [value]);

  function handleCoordinateChange(axis: "lat" | "lng", raw: string): void {
    const parsed = raw === "" ? undefined : Number(raw);
    const clamped =
      parsed === undefined || Number.isNaN(parsed) ? undefined : (axis === "lat" ? clampLat : clampLng)(parsed);
    onChange({ ...value, [axis]: clamped });
  }

  const hasError = error !== undefined;

  return (
    <div className={`flex flex-col gap-2 ${disabled ? "cursor-not-allowed opacity-50" : ""}`}>
      {label !== undefined && ariaLabelledBy === undefined && (
        <span id={id} className="text-sm text-text">
          {label}
        </span>
      )}
      <div
        ref={mapHostRef}
        style={{ height: "240px" }}
        className="overflow-hidden rounded-control border border-border"
      />
      <div className="flex gap-2">
        <span id={`${id}-lat-label`} className="sr-only">
          Latitude
        </span>
        <input
          id={`${id}-lat`}
          type="number"
          step="any"
          min={-90}
          max={90}
          aria-labelledby={labelledById !== undefined ? `${labelledById} ${id}-lat-label` : `${id}-lat-label`}
          aria-invalid={hasError}
          value={value?.lat ?? ""}
          disabled={disabled}
          className={`flex-1 ${fieldInputClassName(hasError, "input", "sans")}`}
          onChange={(e) => handleCoordinateChange("lat", e.target.value)}
        />
        <span id={`${id}-lng-label`} className="sr-only">
          Longitude
        </span>
        <input
          id={`${id}-lng`}
          type="number"
          step="any"
          min={-180}
          max={180}
          aria-labelledby={labelledById !== undefined ? `${labelledById} ${id}-lng-label` : `${id}-lng-label`}
          aria-invalid={hasError}
          value={value?.lng ?? ""}
          disabled={disabled}
          className={`flex-1 ${fieldInputClassName(hasError, "input", "sans")}`}
          onChange={(e) => handleCoordinateChange("lng", e.target.value)}
        />
      </div>
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
    </div>
  );
}
