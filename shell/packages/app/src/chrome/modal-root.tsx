import { type ModalEntry, modalManager } from "@goerp/sdk/realtime";
import { Fragment, type ReactNode, useSyncExternalStore } from "react";

export interface ModalRootProps {
  renderEntry?: (entry: ModalEntry) => ReactNode;
}

export function ModalRoot({ renderEntry }: ModalRootProps) {
  const stack = useSyncExternalStore(modalManager.subscribe, modalManager.getStack);

  return stack.map((entry) => <Fragment key={entry.id}>{renderEntry?.(entry)}</Fragment>);
}
