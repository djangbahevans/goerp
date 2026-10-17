import type { Contrast, Theme } from "@goerp/sdk/react";
import { themeStore } from "@goerp/sdk/react";
import type { A11yParameters } from "@storybook/addon-a11y";
import type { AfterEach } from "@storybook/react-vite";
import axe from "axe-core";
import { waitForAnimations } from "storybook/preview-api";

const VARIANTS: { theme: Theme; contrast: Contrast }[] = [
  { theme: "light", contrast: "standard" },
  { theme: "dark", contrast: "standard" },
  { theme: "light", contrast: "high" },
  { theme: "dark", contrast: "high" },
];

const COLOUR_RULES = ["color-contrast", "link-in-text-block"];

// Storybook's own chrome, which the a11y addon also leaves out of its run.
const STORYBOOK_ELEMENTS = [".sb-wrapper", "#storybook-docs", "#storybook-highlights-root"];

function enabledColourRules(a11y: A11yParameters): string[] {
  const disabled = new Set(a11y.config?.rules?.filter((rule) => rule.enabled === false).map((rule) => rule.id));
  return COLOUR_RULES.filter((id) => !disabled.has(id) && a11y.options?.rules?.[id]?.enabled !== false);
}

async function settle(): Promise<void> {
  await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  await waitForAnimations();
}

// The a11y addon runs every rule in the story's own theme. A theme changes only colours, so each
// other shipped theme reruns just the colour rules. Interactive Storybook skips this: switching
// themes after every render would flash the canvas.
export const checkContrastInEveryTheme: AfterEach = async ({ parameters, viewMode }) => {
  const a11y: A11yParameters | undefined = parameters.a11y;
  if (import.meta.env.VITEST_STORYBOOK === undefined || viewMode !== "story") return;
  if (a11y === undefined || a11y.disable === true || a11y.test !== "error") return;
  const rules = enabledColourRules(a11y);
  if (rules.length === 0) return;

  const original = { theme: themeStore.getTheme(), contrast: themeStore.getContrast() };
  const failures: string[] = [];
  try {
    for (const variant of VARIANTS) {
      if (variant.theme === original.theme && variant.contrast === original.contrast) continue;
      themeStore.setTheme(variant.theme);
      themeStore.setContrastPreference(variant.contrast);
      await settle();
      axe.reset();
      axe.configure(a11y.config ?? {});
      const result = await axe.run(
        { include: document.body, exclude: STORYBOOK_ELEMENTS },
        { runOnly: { type: "rule", values: rules } },
      );
      for (const violation of result.violations) {
        for (const node of violation.nodes) {
          failures.push(
            `${violation.id} in ${variant.theme}/${variant.contrast} contrast at ${node.target.join(" ")}\n  ${node.html}\n  ${node.failureSummary ?? ""}`,
          );
        }
      }
    }
  } finally {
    themeStore.setTheme(original.theme);
    themeStore.setContrastPreference(original.contrast);
  }
  if (failures.length > 0) {
    throw new Error(`Accessibility violations outside the story's theme:\n\n${failures.join("\n\n")}`);
  }
};
