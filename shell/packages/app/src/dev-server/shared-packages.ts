import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { resolve } from "node:path";
import type { Plugin } from "vite";

const SDK_EXPORTS = Object.keys(
  JSON.parse(readFileSync(new URL("../../../sdk/package.json", import.meta.url), "utf8")).exports,
).map((key) => (key === "." ? "@goerp/sdk" : `@goerp/sdk${key.slice(1)}`));

export const SHARED_PACKAGES = [
  "react",
  "react-dom",
  "react-dom/client",
  "react/jsx-runtime",
  "react/jsx-dev-runtime",
  "@tanstack/react-query",
  ...SDK_EXPORTS,
];

const PREFIX = "virtual:goerp-shared/";
const PLACEHOLDER = "<!--goerp-import-map-->";
const defaultExports = new Set(["react", "react-dom", "react-dom/client"]);
const require = createRequire(import.meta.url);
const commonJSExports = new Map(
  SHARED_PACKAGES.filter((name) => name === "react" || name.startsWith("react/") || name.startsWith("react-dom")).map(
    (name) => [name, Object.keys(require(name)).filter((key) => key !== "default" && key !== "__esModule")],
  ),
);

export function sharedPackages(): Plugin {
  let command: string;
  const entries = new Map<string, string>();
  const devMap = {
    imports: Object.fromEntries(SHARED_PACKAGES.map((name) => [name, `/@id/__x00__${PREFIX}${name}`])),
  };

  return {
    name: "vite-plugin-goerp-shared-packages",
    config(config, env) {
      command = env.command;
      return {
        resolve: { dedupe: SHARED_PACKAGES },
        optimizeDeps: { include: SHARED_PACKAGES },
        build: { rolldownOptions: { input: resolve(config.root ?? process.cwd(), "index.html") } },
      };
    },
    resolveId(id) {
      if (id.startsWith(PREFIX) && SHARED_PACKAGES.includes(id.slice(PREFIX.length))) return `\0${id}`;
    },
    load(id) {
      if (!id.startsWith(`\0${PREFIX}`)) return;
      const name = id.slice(PREFIX.length + 1);
      // Vite cannot forward CommonJS named exports through an export-star wrapper.
      const names = commonJSExports.get(name);
      return `export ${names ? `{ ${names.join(", ")} }` : "*"} from ${JSON.stringify(name)};${
        defaultExports.has(name) ? `export { default } from ${JSON.stringify(name)};` : ""
      }`;
    },
    buildStart() {
      if (command !== "build") return;
      for (const name of SHARED_PACKAGES) {
        entries.set(
          name,
          this.emitFile({
            type: "chunk",
            id: `${PREFIX}${name}`,
            name: `shared-${name.replace(/[^a-zA-Z0-9]/g, "-")}`,
            preserveSignature: "strict",
          }),
        );
      }
    },
    generateBundle() {
      const imports = Object.fromEntries([...entries].map(([name, ref]) => [name, `/${this.getFileName(ref)}`]));
      this.emitFile({ type: "asset", fileName: "import-map.json", source: JSON.stringify({ imports }) });
    },
    configureServer(server) {
      server.middlewares.use("/__goerp_import_map", (_req, res) => {
        res.setHeader("Content-Type", "application/json");
        res.setHeader("Cache-Control", "no-store");
        res.end(JSON.stringify(devMap));
      });
    },
    transformIndexHtml: {
      order: "post",
      handler(html, ctx) {
        if (command === "serve") {
          return html.replace(
            PLACEHOLDER,
            `<script type="importmap" id="goerp-import-map">${JSON.stringify(devMap)}</script>`,
          );
        }
        // Native imports of shared entries do not load their extracted CSS.
        const styles = Object.values(ctx.bundle ?? {})
          .filter((entry) => entry.type === "asset" && entry.fileName.endsWith(".css"))
          .filter((entry) => !html.includes(`/${entry.fileName}`))
          .map((entry) => ({
            tag: "link",
            attrs: { rel: "stylesheet", href: `/${entry.fileName}` },
            injectTo: "head" as const,
          }));
        return { html, tags: styles };
      },
    },
  };
}
