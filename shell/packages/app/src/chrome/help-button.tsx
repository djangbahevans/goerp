import { IconButton } from "@goerp/sdk/components";
import { type ReactNode, useState } from "react";
import { HelpPanel } from "./help-panel.js";

// help-panel.md.
export function HelpButton(): ReactNode {
  const [open, setOpen] = useState(false);
  return (
    <>
      <IconButton icon="circle-help" label="Help" aria-expanded={open} onClick={() => setOpen((current) => !current)} />
      <HelpPanel open={open} onClose={() => setOpen(false)} />
    </>
  );
}
