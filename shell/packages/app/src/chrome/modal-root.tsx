import { type ModalEntry, modalManager } from "@goerp/sdk/realtime";
import { Fragment, type ReactNode, useSyncExternalStore } from "react";
import { DialogModal } from "./dialog-modal.js";
import { FormModal } from "./form-modal.js";

type FormEntry = Extract<ModalEntry, { kind: "form" }>;

export interface ModalRootProps {
  // Replaces FormModal, for a test fixture that has no view registry.
  renderForm?: (entry: FormEntry) => ReactNode;
}

export function ModalRoot({ renderForm = (entry) => <FormModal entry={entry} /> }: ModalRootProps) {
  const stack = useSyncExternalStore(modalManager.subscribe, modalManager.getStack);

  return stack.map((entry) => (
    <Fragment key={entry.id}>{entry.kind === "dialog" ? <DialogModal entry={entry} /> : renderForm(entry)}</Fragment>
  ));
}
