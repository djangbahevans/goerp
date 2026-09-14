import { createContext, type ReactNode, useContext } from "react";
import type { ActionMenuItem } from "../components/action-menu.js";

export interface KanbanCardContextValue {
  isDragging: boolean;
  // card_actions resolved into plain menu items — a card_component override
  // replaces the generic card body entirely, so this is its only way to
  // still reach and render them.
  actions: ActionMenuItem[];
}

const KanbanCardContext = createContext<KanbanCardContextValue>({ isDragging: false, actions: [] });

export interface KanbanCardProviderProps {
  isDragging: boolean;
  actions?: ActionMenuItem[];
  children: ReactNode;
}

// The kanban renderer (shell/packages/app's KanbanCard, not this package)
// wraps each card's render slot in this so a manifest's custom
// card_component — living in module code outside this package entirely —
// can read drag state via useKanbanCard() with no prop of its own,
// matching view-system.md §6's documented CRMLeadCard example.
export function KanbanCardProvider({ isDragging, actions = [], children }: KanbanCardProviderProps): ReactNode {
  return <KanbanCardContext.Provider value={{ isDragging, actions }}>{children}</KanbanCardContext.Provider>;
}

export function useKanbanCard(): KanbanCardContextValue {
  return useContext(KanbanCardContext);
}
