package module

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func devFrontendConfig(moduleDir, sessionDir string, opts devOptions) (string, string, error) {
	frontend := filepath.Join(moduleDir, "frontend")
	data, err := os.ReadFile(filepath.Join(moduleDir, "manifest.json"))
	if err != nil {
		return "", "", err
	}

	var manifest struct {
		Frontend struct {
			Entry string `json:"entry"`
		} `json:"frontend"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", "", err
	}

	entry := cmp.Or(manifest.Frontend.Entry, "src/index.ts")
	if filepath.IsAbs(entry) || !filepath.IsLocal(entry) {
		return "", "", fmt.Errorf("frontend entry must be a relative path inside frontend/")
	}
	if info, err := os.Stat(filepath.Join(frontend, entry)); err != nil || info.IsDir() {
		return "", "", fmt.Errorf("frontend entry %q is not a source file", entry)
	}

	frontendJSON, _ := json.Marshal(frontend)
	originJSON, _ := json.Marshal("http://dev.localhost:" + strconv.Itoa(opts.uiPort))
	config := fmt.Sprintf(`import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const require = createRequire(%s + "/package.json");
const { loadConfigFromFile, mergeConfig } = await import(pathToFileURL(require.resolve("vite")));
const loaded = await loadConfigFromFile({ command: "serve", mode: "development" }, undefined, %s);
const shared = ["react", "react-dom", "react-dom/client", "react/jsx-runtime", "react/jsx-dev-runtime", "@tanstack/react-query"];
const sdk = JSON.parse(readFileSync(new URL("../package.json", pathToFileURL(require.resolve("@goerp/sdk"))), "utf8"));
shared.push(...Object.keys(sdk.exports).map(key => key === "." ? "@goerp/sdk" : "@goerp/sdk" + key.slice(1)));
export default mergeConfig(loaded?.config ?? {}, {
  root: %s,
  base: "/__goerp_module/",
  optimizeDeps: { exclude: shared },
  server: { host: "127.0.0.1", port: %d, strictPort: true, allowedHosts: [".localhost"], hmr: { clientPort: %d } },
  plugins: [{ name: "goerp-module-shared-runtime", enforce: "pre", resolveId(id) {
    if (shared.includes(id)) return { id: %s + "/@id/__x00__virtual:goerp-shared/" + id, external: true };
  } }],
});
`, frontendJSON, frontendJSON, frontendJSON, opts.modulePort, opts.uiPort, originJSON)

	path := filepath.Join(sessionDir, "module-vite.config.mjs")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		return "", "", fmt.Errorf("write module Vite config: %w", err)
	}

	return path, "/__goerp_module/" + filepath.ToSlash(entry), nil
}
