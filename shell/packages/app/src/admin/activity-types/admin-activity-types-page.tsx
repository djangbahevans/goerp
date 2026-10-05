import {
  closestCenter,
  DndContext,
  type DragEndEvent,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
} from "@dnd-kit/core";
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useTenant } from "@goerp/sdk/auth";
import {
  ActionButton,
  type ActionMenuItem,
  Badge,
  Button,
  EmptyState,
  Icon,
  PageHeader,
  PageLayout,
  Skeleton,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import {
  type AdminActivityType,
  useAdminActivityTypes,
  useDeleteActivityType,
  useReorderActivityTypes,
  useUpdateActivityType,
} from "@goerp/sdk/react";
import { type CSSProperties, type ReactNode, useState } from "react";
import { RowActionMenu } from "../row-action-menu.js";
import { ActivityTypeSheet } from "./activity-type-sheet.js";

// scheduled-activities.md §9 "Limits".
const MAX_TYPES = 50;

function defaultsLabel(type: AdminActivityType, defaultLocale: string): string {
  const summary = type.defaultSummary[defaultLocale];
  const due = type.defaultDueDays;
  if (summary && due !== null) return `${summary} · Due in ${due} days`;
  if (summary) return summary;
  if (due !== null) return `Due in ${due} days`;
  return "—";
}

function usageLabel(count: number): string {
  return count === 1 ? "1 activity" : `${count} activities`;
}

// shell-ux.md §5.10.
export function AdminActivityTypesPage(): ReactNode {
  const tenant = useTenant();
  const query = useAdminActivityTypes();
  const reorder = useReorderActivityTypes();
  const update = useUpdateActivityType();
  const remove = useDeleteActivityType();
  const [sheetOpen, setSheetOpen] = useState(false);
  const [editing, setEditing] = useState<AdminActivityType | null>(null);
  const [announcement, setAnnouncement] = useState("");

  const types = query.data ?? [];
  const activeCount = types.filter((t) => !t.archived).length;
  const atLimit = types.length >= MAX_TYPES;

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  function openAdd(): void {
    setEditing(null);
    setSheetOpen(true);
  }

  function openEdit(type: AdminActivityType): void {
    setEditing(type);
    setSheetOpen(true);
  }

  async function applyOrder(keys: string[], movedKey: string): Promise<void> {
    try {
      await reorder.mutateAsync(keys);
      const moved = types.find((t) => t.key === movedKey);
      const label = moved ? (moved.label[tenant.defaultLocale] ?? moved.key) : movedKey;
      setAnnouncement(`Moved ${label} to position ${keys.indexOf(movedKey) + 1} of ${keys.length}.`);
    } catch {
      toast.error("Couldn't reorder types. Try again.");
    }
  }

  function moveBy(type: AdminActivityType, delta: number): void {
    const index = types.findIndex((t) => t.key === type.key);
    const target = index + delta;
    if (index === -1 || target < 0 || target >= types.length) return;
    void applyOrder(
      arrayMove(types, index, target).map((t) => t.key),
      type.key,
    );
  }

  function handleDragEnd(event: DragEndEvent): void {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const oldIndex = types.findIndex((t) => t.key === active.id);
    const newIndex = types.findIndex((t) => t.key === over.id);
    if (oldIndex === -1 || newIndex === -1) return;
    void applyOrder(
      arrayMove(types, oldIndex, newIndex).map((t) => t.key),
      String(active.id),
    );
  }

  async function toggleArchive(type: AdminActivityType): Promise<void> {
    try {
      await update.mutateAsync({ key: type.key, changes: { archived: !type.archived } });
    } catch (err) {
      toast.error(
        err instanceof AppError && err.code === "last_active_type"
          ? "At least one type must stay active."
          : "Couldn't update this type.",
      );
    }
  }

  async function deleteType(type: AdminActivityType): Promise<void> {
    try {
      await remove.mutateAsync(type.key);
    } catch {
      toast.error("Couldn't delete this type.");
    }
  }

  return (
    <PageLayout>
      <PageHeader
        title="Activity types"
        subtitle="The types available when scheduling an activity, and their labels and icons across the app."
        actions={
          <Button variant="primary" onClick={openAdd} disabled={atLimit}>
            Add type
          </Button>
        }
      />
      {atLimit && <p className="text-sm text-text-secondary">A workspace can have at most 50 activity types.</p>}
      <div aria-live="polite" className="sr-only">
        {announcement}
      </div>
      {query.isLoading ? (
        <Skeleton type="table" columns={6} />
      ) : query.isError ? (
        <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-text">Couldn't load activity types.</p>
          <ActionButton variant="secondary" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      ) : types.length === 0 ? (
        <EmptyState
          icon="calendar-check"
          title="No activity types"
          action={<Button onClick={openAdd}>Add type</Button>}
        />
      ) : (
        <div className="overflow-x-auto">
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
            <table className="w-full border-collapse">
              <thead>
                <tr className="border-border border-b bg-surface">
                  <th scope="col" className="w-8 p-3" />
                  <th scope="col" className="w-8 p-3" />
                  <th scope="col" className="p-3 text-left font-medium text-sm text-text-secondary">
                    Label
                  </th>
                  <th scope="col" className="p-3 text-left font-medium text-sm text-text-secondary">
                    Key
                  </th>
                  <th scope="col" className="p-3 text-left font-medium text-sm text-text-secondary">
                    Defaults
                  </th>
                  <th scope="col" className="p-3 text-left font-medium text-sm text-text-secondary">
                    Usage
                  </th>
                  <th scope="col" className="w-8 p-3" />
                </tr>
              </thead>
              <SortableContext items={types.map((t) => t.key)} strategy={verticalListSortingStrategy}>
                <tbody>
                  {types.map((type, index) => (
                    <TypeRow
                      key={type.key}
                      type={type}
                      index={index}
                      total={types.length}
                      defaultLocale={tenant.defaultLocale}
                      canArchive={type.archived || activeCount > 1}
                      onEdit={() => openEdit(type)}
                      onMoveUp={() => moveBy(type, -1)}
                      onMoveDown={() => moveBy(type, 1)}
                      onToggleArchive={() => void toggleArchive(type)}
                      onDelete={() => void deleteType(type)}
                    />
                  ))}
                </tbody>
              </SortableContext>
            </table>
          </DndContext>
        </div>
      )}
      <ActivityTypeSheet open={sheetOpen} onClose={() => setSheetOpen(false)} editing={editing} />
    </PageLayout>
  );
}

interface TypeRowProps {
  type: AdminActivityType;
  index: number;
  total: number;
  defaultLocale: string;
  canArchive: boolean;
  onEdit: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onToggleArchive: () => void;
  onDelete: () => void;
}

function TypeRow({
  type,
  index,
  total,
  defaultLocale,
  canArchive,
  onEdit,
  onMoveUp,
  onMoveDown,
  onToggleArchive,
  onDelete,
}: TypeRowProps): ReactNode {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: type.key });
  const style: CSSProperties = { transform: CSS.Transform.toString(transform), transition };
  const label = type.label[defaultLocale] ?? type.key;

  const items: ActionMenuItem[] = [
    { label: "Edit", icon: "pencil", onClick: onEdit },
    { label: "Move up", icon: "arrow-up", onClick: onMoveUp, disabled: index === 0 },
    { label: "Move down", icon: "arrow-down", onClick: onMoveDown, disabled: index === total - 1 },
    { type: "separator" },
    type.archived
      ? { label: "Restore", icon: "rotate-ccw", onClick: onToggleArchive }
      : { label: "Archive", icon: "archive", onClick: onToggleArchive, disabled: !canArchive },
    {
      label: "Delete",
      icon: "trash-2",
      variant: "danger",
      disabled: type.usageCount > 0,
      confirm: { title: `Delete ${label}?`, message: "This can't be undone.", destructive: true },
      onClick: onDelete,
    },
  ];

  return (
    <tr
      ref={setNodeRef}
      style={style}
      className={`border-border border-b bg-surface ${isDragging ? "opacity-50" : ""}`}
    >
      <td className="p-3">
        <button
          type="button"
          {...attributes}
          {...listeners}
          aria-label={`Reorder ${label}`}
          className="cursor-grab text-text-secondary hover:text-text"
        >
          <Icon name="grip-vertical" size={16} aria-hidden="true" />
        </button>
      </td>
      <td className="p-3">
        <Icon name={type.icon} size={16} aria-hidden="true" />
      </td>
      <td className="p-3 text-text">
        <span className="flex items-center gap-2">
          {label}
          {type.archived && <Badge label="Archived" color="gray" />}
        </span>
      </td>
      <td className="p-3 font-mono text-text-secondary text-xs">{type.key}</td>
      <td className="p-3 text-sm text-text-secondary">{defaultsLabel(type, defaultLocale)}</td>
      <td className="p-3 text-sm text-text-secondary">{usageLabel(type.usageCount)}</td>
      <td className="p-3 text-right">
        <RowActionMenu label={`${label} actions`} items={items} />
      </td>
    </tr>
  );
}
