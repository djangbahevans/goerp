import type { Plugin } from "vite";

const ID = "virtual:goerp-module-development";

export function moduleDevelopment(): Plugin {
  let development = false;
  let origin = "";
  return {
    name: "goerp-module-development",
    config(_config, env) {
      development = env.command === "serve";
    },
    configResolved(config) {
      origin = `http://dev.localhost:${config.server.port}`;
    },
    resolveId(id) {
      if (id === ID) return `\0${ID}`;
      if (development && process.env.GOERP_MODULE_FRONTEND_NAME && id.startsWith("/__goerp_module/")) {
        return { id: `${origin}${id}`, external: true };
      }
    },
    load(id) {
      if (id !== `\0${ID}`) return;
      const name = development ? process.env.GOERP_MODULE_FRONTEND_NAME : undefined;
      const entry = development ? process.env.GOERP_MODULE_FRONTEND_ENTRY : undefined;
      if (!name || !entry)
        return "export const developmentModuleName = null; export async function loadDevelopmentModule() { return null; }";
      if (!entry.startsWith("/__goerp_module/") || entry.includes("..")) {
        throw new Error("Module development entry must use the local module proxy");
      }
      return `import "/__goerp_module/@vite/client";
export const developmentModuleName = ${JSON.stringify(name)};
const entry = ${JSON.stringify(entry)};
export async function loadDevelopmentModule() { return import(/* @vite-ignore */ entry); }`;
    },
  };
}
