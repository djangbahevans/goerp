import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app";
import "./design/tokens.css";
// LocationField (@goerp/sdk/components) renders a MapLibre GL JS map — the
// SDK package builds with plain tsc, not a bundler, so it can't ship this
// CSS itself; the app entry point is where every other global style import
// already lives (tokens.css, above).
import "maplibre-gl/dist/maplibre-gl.css";
// Constructing localeStore sets <html lang/dir> before the first render.
import "@goerp/sdk/i18n";

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Root element #root not found");
}

createRoot(rootElement).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
