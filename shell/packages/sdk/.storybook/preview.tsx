import type { Decorator, Preview } from "@storybook/react-vite";
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

// Toggles data-theme on a wrapper element — [data-theme="dark"] is already
// defined by shell/packages/app/src/design/themes/dark.css, so this needs
// no dependency on a full app-wide theme system (none exists yet).
const withTheme: Decorator = (Story, context) => {
  const theme = context.globals.theme === "dark" ? "dark" : "light";
  return (
    <div data-theme={theme} style={{ background: "var(--color-bg)", color: "var(--color-fg)", padding: "1rem" }}>
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
