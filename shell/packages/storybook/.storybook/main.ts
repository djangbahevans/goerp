import { fileURLToPath } from "node:url";
import type { StorybookConfig } from "@storybook/react-vite";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";

const sdkSrc = fileURLToPath(new URL("../../sdk/src", import.meta.url));

const config: StorybookConfig = {
  stories: ["../../*/src/**/*.stories.tsx"],
  framework: "@storybook/react-vite",
  viteFinal: async (viteConfig) => {
    viteConfig.plugins = [...(viteConfig.plugins ?? []), react(), tailwindcss()];
    // @goerp/sdk's package.json `exports` map every subpath to compiled
    // dist/ output. A story living inside packages/sdk/src imports its own
    // component via a relative path instead, which Vite resolves straight
    // to that raw src file — so a component reading a React Context
    // (PermissionContext, ...) sees a different module instance depending
    // on which way it was reached, and useContext finds no matching
    // Provider even though the global decorator wraps one around every
    // story. Aliasing the package name to source too keeps both paths on
    // the same module instance.
    viteConfig.resolve = {
      ...viteConfig.resolve,
      alias: [
        ...(Array.isArray(viteConfig.resolve?.alias) ? viteConfig.resolve.alias : []),
        { find: /^@goerp\/sdk$/, replacement: `${sdkSrc}/index.ts` },
        { find: /^@goerp\/sdk\/(.+)$/, replacement: `${sdkSrc}/$1/index.ts` },
      ],
    };
    return viteConfig;
  },
};

export default config;
