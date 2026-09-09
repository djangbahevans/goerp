import type { Decorator, Preview } from "@storybook/react-vite";
import { useEffect } from "react";
import { PermissionContext } from "../../sdk/src/auth/permission-provider.js";
import "../../app/src/design/tokens.css";

// Always-allow: permission gating itself is unit-tested, not story-tested,
// and useOptionalPermission throws outside a PermissionContext at all.
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

// Sets data-theme on document.documentElement, not a wrapper div: Tailwind's
// @theme block resolves --color-* to var(--goerp-color-*) once on :root, so
// an override below :root never reaches it.
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
