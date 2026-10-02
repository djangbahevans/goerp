import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app";
import { watchImportMap } from "./bootstrap/import-map.js";
import "./design/tokens.css";
// The SDK's tsc build leaves stylesheet loading to the shell.
import "maplibre-gl/dist/maplibre-gl.css";
// Constructing localeStore sets <html lang/dir> before the first render.
import "@goerp/sdk/i18n";

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Root element #root not found");
}

const stopWatchingImportMap = watchImportMap();
if (import.meta.hot) import.meta.hot.dispose(stopWatchingImportMap);

createRoot(rootElement).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
