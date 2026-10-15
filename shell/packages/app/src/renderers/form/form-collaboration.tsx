import type { FieldOf, ResourceName } from "@goerp/sdk";
import { AuthContext } from "@goerp/sdk/auth";
import { Button, Icon, UserAvatar } from "@goerp/sdk/components";
import { usePresence } from "@goerp/sdk/realtime";
import { useContext } from "react";

export function FormCollaborators({
  resource,
  recordId,
  editingField,
}: {
  resource: string;
  recordId: string;
  editingField: string | null;
}) {
  const users = usePresence(resource as ResourceName, recordId, {
    editingField: editingField as FieldOf<ResourceName> | null,
  });
  const currentUserId = useContext(AuthContext)?.user?.id;
  const others = users.filter((user) => user.userId !== currentUserId);
  if (others.length === 0) return null;
  return (
    <ul aria-label="Also on this record" className="flex items-center -space-x-1">
      {others.map((user) => (
        <li key={user.userId} className="rounded-full ring-2 ring-bg">
          <UserAvatar userId={user.userId} name={user.name} avatarUrl={user.avatarUrl} size="sm" showTooltip />
        </li>
      ))}
    </ul>
  );
}

export function FormConflictWarning({
  onKeepLocal,
  onLoadSaved,
}: {
  onKeepLocal: () => void;
  onLoadSaved: () => void;
}) {
  return (
    <div
      role="alert"
      className="flex flex-wrap items-center gap-3 rounded-structural border border-border bg-surface p-4 text-sm text-text"
    >
      <Icon name="triangle-alert" size={20} className="shrink-0 text-warning" aria-hidden="true" />
      <p className="min-w-0 flex-1">Someone else saved changes to fields you're editing.</p>
      <div className="flex flex-wrap gap-2">
        <Button variant="secondary" onClick={onKeepLocal}>
          Keep my changes
        </Button>
        <Button variant="secondary" onClick={onLoadSaved}>
          Load saved version
        </Button>
      </div>
    </div>
  );
}
