import {
  ActionButton,
  AlertDialog,
  Badge,
  Button,
  EmptyState,
  FieldWrapper,
  Icon,
  PageLayout,
  SectionCard,
  Skeleton,
  TextInput,
  TextLink,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { type FormEvent, type MouseEvent, type ReactNode, useMemo, useState } from "react";
import { roleLabel } from "../users/roles.js";
import {
  type AdminRoleDetail,
  type CatalogPermission,
  type RoleInput,
  useAdminRole,
  useCreateRole,
  useDeleteRole,
  usePermissionCatalog,
  useUpdateRole,
} from "./admin-roles-api.js";
import { userCountLabel } from "./admin-roles-page.js";
import { buildMatrix, matrixNames, togglePermission } from "./permission-matrix.js";
import { PermissionMatrixView } from "./permission-matrix-view.js";

// auth-internals.md §10 "Tenant admin role endpoints" rules.
const NAME_PATTERN = /^[a-z][a-z0-9_]{0,62}$/;
const RESERVED_NAMES = new Set(["superadmin", "public"]);
const MAX_DESCRIPTION = 500;

interface FormErrors {
  name?: string;
  description?: string;
}

function validate(input: RoleInput): FormErrors {
  const errors: FormErrors = {};
  if (!NAME_PATTERN.test(input.name)) {
    errors.name = "Use 1–63 lowercase letters, digits or underscores, starting with a letter.";
  } else if (RESERVED_NAMES.has(input.name)) {
    errors.name = "This name is reserved.";
  }
  if ([...input.description].length > MAX_DESCRIPTION) {
    errors.description = `Keep the description to ${MAX_DESCRIPTION} characters.`;
  }
  return errors;
}

function errorsFor(err: unknown): FormErrors | null {
  if (!(err instanceof AppError)) return null;
  switch (err.code) {
    case "invalid_name":
      return { name: "Use 1–63 lowercase letters, digits or underscores, starting with a letter." };
    case "role_name_taken":
      return { name: "Another role already has this name." };
    case "invalid_description":
      return { description: `Keep the description to ${MAX_DESCRIPTION} characters.` };
  }
  return null;
}

function failureMessage(err: unknown, fallback: string): string {
  return err instanceof AppError && err.message ? err.message : fallback;
}

function sameSet(a: ReadonlySet<string>, b: readonly string[]): boolean {
  return a.size === b.length && b.every((name) => a.has(name));
}

// A plain left click navigates in the shell; a modified click keeps the
// browser's own handling of the link (a new tab, say).
function inShell(event: MouseEvent, navigate: () => void) {
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  event.preventDefault();
  navigate();
}

export function usersWithRoleHref(roleName: string): string {
  return `/admin/users?role=${encodeURIComponent(roleName)}`;
}

function LoadError({ what, onRetry }: { what: string; onRetry: () => void }): ReactNode {
  return (
    <PageLayout>
      <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
        <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
        <p className="text-text">Couldn't load {what}.</p>
        <ActionButton variant="secondary" onClick={onRetry}>
          Retry
        </ActionButton>
      </div>
    </PageLayout>
  );
}

function Loading(): ReactNode {
  return (
    <PageLayout>
      <Skeleton type="card" lines={6} />
    </PageLayout>
  );
}

export interface AdminRoleDetailPageProps {
  roleId: string;
  onBackToList: () => void;
  onOpenUsers: (roleName: string) => void;
}

// shell-ux.md §5.2 "Role detail page".
export function AdminRoleDetailPage({ roleId, onBackToList, onOpenUsers }: AdminRoleDetailPageProps): ReactNode {
  const role = useAdminRole(roleId);
  const catalog = usePermissionCatalog();

  if (role.isLoading || catalog.isLoading) return <Loading />;
  if (role.error instanceof AppError && role.error.httpStatus === 404) {
    return (
      <PageLayout>
        <EmptyState
          icon="shield-off"
          title="Role not found"
          description="It may have been deleted."
          action={
            <Button variant="secondary" onClick={onBackToList}>
              Back to roles
            </Button>
          }
        />
      </PageLayout>
    );
  }
  if (role.isError || !role.data) return <LoadError what="this role" onRetry={() => void role.refetch()} />;
  if (catalog.isError || !catalog.data) {
    return <LoadError what="the permission list" onRetry={() => void catalog.refetch()} />;
  }

  return (
    <RoleForm
      saved={role.data}
      catalog={catalog.data}
      onDeleted={onBackToList}
      onOpenUsers={onOpenUsers}
      onCreated={() => {}}
    />
  );
}

export interface AdminCreateRolePageProps {
  onCreated: (id: string) => void;
}

// shell-ux.md §5.2: "Create role" opens the detail form empty.
export function AdminCreateRolePage({ onCreated }: AdminCreateRolePageProps): ReactNode {
  const catalog = usePermissionCatalog();
  if (catalog.isLoading) return <Loading />;
  if (catalog.isError || !catalog.data) {
    return <LoadError what="the permission list" onRetry={() => void catalog.refetch()} />;
  }
  return (
    <RoleForm saved={null} catalog={catalog.data} onCreated={onCreated} onDeleted={() => {}} onOpenUsers={() => {}} />
  );
}

interface RoleFormProps {
  // null while creating a role.
  saved: AdminRoleDetail | null;
  catalog: CatalogPermission[];
  onCreated: (id: string) => void;
  onDeleted: () => void;
  onOpenUsers: (roleName: string) => void;
}

function toInput(role: AdminRoleDetail | null): RoleInput {
  return { name: role?.name ?? "", description: role?.description ?? "", permissions: role?.permissions ?? [] };
}

function RoleForm({ saved: role, catalog, onCreated, onDeleted, onOpenUsers }: RoleFormProps): ReactNode {
  const creating = role === null;
  const readOnly = role?.isImmutable ?? false;
  const [baseline, setBaseline] = useState(() => toInput(role));
  const [name, setName] = useState(baseline.name);
  const [description, setDescription] = useState(baseline.description);
  const [selected, setSelected] = useState<Set<string>>(() => new Set(baseline.permissions));
  const [errors, setErrors] = useState<FormErrors>({});
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  const create = useCreateRole();
  const update = useUpdateRole(role?.id ?? "");
  const remove = useDeleteRole(role?.id ?? "");

  const sections = useMemo(() => buildMatrix(catalog, baseline.permissions), [catalog, baseline.permissions]);
  const available = useMemo(() => matrixNames(sections), [sections]);

  const nameChanged = name.trim() !== baseline.name;
  const descriptionChanged = description.trim() !== baseline.description;
  const permissionsChanged = !sameSet(selected, baseline.permissions);
  const dirty = creating || nameChanged || descriptionChanged || permissionsChanged;
  const saving = create.isPending || update.isPending;

  const reset = (to: RoleInput) => {
    setBaseline(to);
    setName(to.name);
    setDescription(to.description);
    setSelected(new Set(to.permissions));
    setErrors({});
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (readOnly || !dirty || saving) return;
    const input: RoleInput = { name: name.trim(), description: description.trim(), permissions: [...selected].sort() };
    const invalid = validate(input);
    setErrors(invalid);
    if (invalid.name || invalid.description) return;
    try {
      if (creating) {
        const created = await create.mutateAsync(input);
        toast.success(`Created the ${created.name} role.`);
        onCreated(created.id);
        return;
      }
      const saved = await update.mutateAsync({
        ...(nameChanged ? { name: input.name } : {}),
        ...(descriptionChanged ? { description: input.description } : {}),
        ...(permissionsChanged ? { permissions: input.permissions } : {}),
      });
      reset(toInput(saved));
      toast.success("Saved changes.");
    } catch (err) {
      const fieldErrors = errorsFor(err);
      if (fieldErrors) setErrors(fieldErrors);
      else toast.error(failureMessage(err, creating ? "The role couldn't be created." : "Changes couldn't be saved."));
    }
  };

  const deleteRole = async () => {
    setConfirmingDelete(false);
    try {
      await remove.mutateAsync();
      toast.success(`Deleted the ${baseline.name} role.`);
      onDeleted();
    } catch (err) {
      toast.error(failureMessage(err, "The role couldn't be deleted."));
    }
  };

  const deleteBlocker = (() => {
    if (!role || readOnly) return null;
    if (role.userCount > 0) return `Remove it from its ${userCountLabel(role.userCount)} before deleting it.`;
    if (role.invitationCount > 0) {
      return role.invitationCount === 1
        ? "A pending invitation offers this role. Revoke it before deleting the role."
        : `${role.invitationCount} pending invitations offer this role. Revoke them before deleting the role.`;
    }
    return null;
  })();

  return (
    <PageLayout>
      <form className="flex flex-col gap-6" onSubmit={(event) => void submit(event)} noValidate>
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="flex flex-col gap-1">
            {creating ? (
              <h1 className="font-semibold text-text text-xl">New role</h1>
            ) : (
              <h1 className="flex items-center gap-2 font-semibold text-text text-xl">
                {roleLabel(baseline.name)}
                {readOnly && <Badge label="System" color="gray" />}
              </h1>
            )}
            {role && (
              <p className="text-sm text-text-secondary">
                {role.userCount === 0 ? (
                  "No users have this role."
                ) : (
                  <TextLink
                    href={usersWithRoleHref(role.name)}
                    onClick={(event) => inShell(event, () => onOpenUsers(role.name))}
                  >
                    {userCountLabel(role.userCount)}
                  </TextLink>
                )}
              </p>
            )}
          </div>
          {role && !readOnly && (
            <div className="flex flex-col items-end gap-1">
              <ActionButton
                variant="danger"
                disabled={deleteBlocker !== null}
                loading={remove.isPending}
                onClick={() => setConfirmingDelete(true)}
              >
                Delete role
              </ActionButton>
              {deleteBlocker && <p className="max-w-xs text-end text-sm text-text-secondary">{deleteBlocker}</p>}
            </div>
          )}
        </div>

        {readOnly && (
          <p className="text-sm text-text-secondary">
            {roleLabel(baseline.name)} is a built-in role. Its permissions come from the modules and can't be changed
            here, and it can't be deleted.
          </p>
        )}

        <SectionCard title="Details">
          {readOnly ? (
            <p className="text-text">{baseline.description || "No description."}</p>
          ) : (
            <div className="flex max-w-lg flex-col gap-4">
              <FieldWrapper
                label="Name"
                required
                description="Lowercase letters, digits and underscores, such as sales_manager."
                {...(errors.name ? { error: errors.name } : {})}
              >
                <TextInput value={name} onChange={setName} autoComplete="off" spellCheck={false} />
              </FieldWrapper>
              <FieldWrapper label="Description" {...(errors.description ? { error: errors.description } : {})}>
                <TextInput value={description} onChange={setDescription} autoComplete="off" />
              </FieldWrapper>
            </div>
          )}
        </SectionCard>

        <SectionCard title="Permissions">
          {!readOnly && (
            <p className="mb-4 text-sm text-text-secondary">
              Granting a permission also grants its group's read permission, and removing read removes the rest of its
              group.
            </p>
          )}
          <PermissionMatrixView
            sections={sections}
            selected={selected}
            readOnly={readOnly}
            onToggle={(permission, checked) =>
              setSelected((current) => togglePermission(current, permission, checked, available))
            }
          />
        </SectionCard>

        {!readOnly && (
          <div className="sticky bottom-0 flex justify-end gap-2 border-border border-t bg-bg py-3">
            {!creating && dirty && (
              <Button variant="secondary" onClick={() => reset(baseline)} disabled={saving}>
                Discard changes
              </Button>
            )}
            <Button type="submit" variant="primary" disabled={!dirty} loading={saving}>
              {creating ? "Create role" : "Save changes"}
            </Button>
          </div>
        )}
      </form>

      {role && !readOnly && (
        <AlertDialog
          open={confirmingDelete}
          title={`Delete the ${baseline.name} role?`}
          description="This can't be undone."
          tone="danger"
          confirmLabel="Delete role"
          confirmVariant="danger"
          onCancel={() => setConfirmingDelete(false)}
          onConfirm={() => void deleteRole()}
        />
      )}
    </PageLayout>
  );
}
