import type { Decorator, Preview } from "@storybook/react-vite";
import { useEffect } from "react";
import { PermissionContext } from "../src/auth/permission-provider.js";
import "../../app/src/design/tokens.css";

// ActionButton/ActionMenu (via useOptionalPermission) throw outside a
// PermissionContext — stories aren't exercising permission gating itself
// (that's unit-tested), so every story renders inside an always-allow
// context rather than repeating this per story.
const permissiveContext = {
  permissions: new Set<string>(),
  fieldAccess: {},
  modulesEnabled: new Set<string>(),
  check: () => true,
  checkField: () => true,
  moduleEnabled: () => true,
};

const withPermissions: Decorator = (Story) => (
  <PermissionContext.Provider value={permissiveContext}>
    <Story />
  </PermissionContext.Provider>
);

// Sets data-theme on the iframe's own <html> (document.documentElement),
// not on a wrapper div: tailwind.config.ts's `@theme` block resolves
// --color-bg/-primary/etc. to `var(--goerp-color-bg)` once, on `:root`
// (Tailwind always emits `@theme` output there). A CSS custom property
// inherits its parent's already-resolved value rather than re-evaluating
// var() references locally, so overriding --goerp-color-* below :root
// never reaches the Tailwind-facing --color-* names components actually
// consume — [data-theme="dark"] has to land on the same element as :root
// for the override to take effect, matching shell-architecture.md's
// documented `document.documentElement.setAttribute('data-theme', ...)`.
const withTheme: Decorator = (Story, context) => {
  const theme = context.globals.theme === "dark" ? "dark" : "light";
  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
  }, [theme]);
  return (
    <div style={{ background: "var(--color-bg)", color: "var(--color-text)", padding: "1rem" }}>
      <Story />
    </div>
  );
};

const preview: Preview = {
  globalTypes: {
    theme: {
      description: "Light/dark theme",
      toolbar: {
        title: "Theme",
        icon: "circlehollow",
        items: [
          { value: "light", icon: "sun", title: "Light" },
          { value: "dark", icon: "moon", title: "Dark" },
        ],
        dynamicTitle: true,
      },
    },
  },
  initialGlobals: {
    theme: "light",
  },
  decorators: [withTheme, withPermissions],
};

export default preview;
