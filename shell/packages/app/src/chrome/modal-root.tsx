import { type FormModalConfig, modalManager } from "@goerp/sdk/realtime";
import { Fragment, type ReactNode, useSyncExternalStore } from "react";
import { DialogModal } from "./dialog-modal.js";

export interface ModalRootProps {
  renderForm?: (config: FormModalConfig) => ReactNode;
}

export function ModalRoot({ renderForm }: ModalRootProps) {
  const stack = useSyncExternalStore(modalManager.subscribe, modalManager.getStack);

  return stack.map((entry) => (
    <Fragment key={entry.id}>
      {entry.kind === "dialog" ? <DialogModal entry={entry} /> : renderForm?.(entry.config)}
    </Fragment>
  ));
}
