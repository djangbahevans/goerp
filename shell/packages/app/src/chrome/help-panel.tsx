import { Icon, type IconName } from "@goerp/sdk/components";
import { ExternalLink } from "lucide-react";
import { type ReactNode, useId } from "react";
import { KeyboardShortcutsList } from "../shortcuts/keyboard-shortcuts-list.js";
import { SideSheet } from "./side-sheet.js";

export interface HelpLink {
  label: string;
  icon: IconName;
  href: string | undefined;
}

export const HELP_LINKS: readonly HelpLink[] = [
  { label: "Documentation", icon: "book-open", href: import.meta.env.VITE_DOCS_URL },
  { label: "API reference", icon: "code", href: import.meta.env.VITE_API_REFERENCE_URL },
  { label: "Status page", icon: "activity", href: import.meta.env.VITE_STATUS_PAGE_URL },
  { label: "What's new", icon: "sparkles", href: import.meta.env.VITE_CHANGELOG_URL },
];

const SECTION_HEADING_CLASSES = "font-semibold text-sm text-text";

export interface HelpPanelProps {
  open: boolean;
  onClose: () => void;
  links?: readonly HelpLink[] | undefined;
}

// help-panel.md: shell-ux.md §7.4's help slide-over.
export function HelpPanel({ open, onClose, links = HELP_LINKS }: HelpPanelProps): ReactNode {
  const linksHeadingId = useId();
  const shortcutsHeadingId = useId();
  const configured = links.flatMap((link) => {
    const href = link.href?.trim();
    return href ? [{ ...link, href }] : [];
  });

  return (
    <SideSheet open={open} onClose={onClose} title="Help">
      <div className="flex flex-col gap-4 p-4">
        {configured.length > 0 && (
          <section aria-labelledby={linksHeadingId}>
            <h3 id={linksHeadingId} className={SECTION_HEADING_CLASSES}>
              Links
            </h3>
            <ul className="-mx-2 mt-1">
              {configured.map((link) => (
                <li key={link.label}>
                  <a
                    href={link.href}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex items-center gap-3 rounded-control p-2 text-sm text-text hover:bg-surface-hover focus-visible:outline-none focus-visible:shadow-focus"
                  >
                    <Icon name={link.icon} size={16} aria-hidden="true" className="text-text-secondary" />
                    <span className="flex-1">{link.label}</span>
                    <ExternalLink size={12} aria-hidden="true" className="text-text-secondary" />
                    <span className="sr-only"> (opens in a new tab)</span>
                  </a>
                </li>
              ))}
            </ul>
          </section>
        )}
        <section aria-labelledby={shortcutsHeadingId}>
          <h3 id={shortcutsHeadingId} className={`${SECTION_HEADING_CLASSES} mb-2`}>
            Keyboard shortcuts
          </h3>
          <KeyboardShortcutsList headingLevel={4} />
        </section>
      </div>
    </SideSheet>
  );
}
