import { createContext, type ReactNode, useContext } from "react";

export interface KanbanCardContextValue {
  isDragging: boolean;
}

const KanbanCardContext = createContext<KanbanCardContextValue>({ isDragging: false });

export interface KanbanCardProviderProps {
  isDragging: boolean;
  children: ReactNode;
}

// The kanban renderer (shell/packages/app's KanbanCard, not this package)
// wraps each card's render slot in this so a manifest's custom
// card_component — living in module code outside this package entirely —
// can read drag state via useKanbanCard() with no prop of its own,
// matching view-system.md §6's documented CRMLeadCard example.
export function KanbanCardProvider({ isDragging, children }: KanbanCardProviderProps): ReactNode {
  return <KanbanCardContext.Provider value={{ isDragging }}>{children}</KanbanCardContext.Provider>;
}

export function useKanbanCard(): KanbanCardContextValue {
  return useContext(KanbanCardContext);
}
