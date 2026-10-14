import { createContext, type DragEvent, type ReactNode, useContext } from "react";
import type { ActionMenuItem } from "../components/action-menu.js";

export interface KanbanCardContextValue {
  isDragging: boolean;
  isDropTarget: boolean;
  columnId: string;
  onDragStart: (event: DragEvent) => void;
  actions: ActionMenuItem[];
}

const KanbanCardContext = createContext<KanbanCardContextValue>({
  isDragging: false,
  isDropTarget: false,
  columnId: "",
  onDragStart: () => {},
  actions: [],
});

export interface KanbanCardProviderProps {
  isDragging: boolean;
  isDropTarget: boolean;
  columnId: string;
  onDragStart: (event: DragEvent) => void;
  actions?: ActionMenuItem[];
  children: ReactNode;
}

export function KanbanCardProvider({
  isDragging,
  isDropTarget,
  columnId,
  onDragStart,
  actions = [],
  children,
}: KanbanCardProviderProps): ReactNode {
  return (
    <KanbanCardContext.Provider value={{ isDragging, isDropTarget, columnId, onDragStart, actions }}>
      {children}
    </KanbanCardContext.Provider>
  );
}

export function useKanbanCard(): KanbanCardContextValue {
  return useContext(KanbanCardContext);
}
