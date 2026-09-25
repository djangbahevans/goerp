import { ActionButton, Button, FieldWrapper, Icon, Select, TextInput } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { type FormEvent, type ReactNode, useEffect, useRef, useState } from "react";
import { SideSheet } from "../../chrome/side-sheet.js";
import { type InviteUserResult, useInviteUser } from "./admin-users-api.js";
import { DEFAULT_INVITE_ROLE, useAssignableRoles } from "./roles.js";

export interface InviteUserSheetProps {
  open: boolean;
  onClose: () => void;
}

interface FormErrors {
  email?: string;
  role?: string;
  form?: string;
}

function errorsFor(err: unknown): FormErrors {
  if (err instanceof AppError) {
    switch (err.code) {
      case "invalid_email":
        return { email: "Enter a valid email address." };
      case "already_member":
        return { email: "This person is already a member of this organisation." };
      case "invalid_role":
        return { role: "Choose a role." };
    }
  }
  return { form: "The invitation couldn't be sent. Try again." };
}

// shell-ux.md §5.1 "Invite slide-over". Whether the invitee already has a
// GoERP account is only known once the invite is sent, so the sheet says so
// on its success view rather than while typing.
export function InviteUserSheet({ open, onClose }: InviteUserSheetProps): ReactNode {
  const invite = useInviteUser();
  const roles = useAssignableRoles();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState(DEFAULT_INVITE_ROLE);
  const [errors, setErrors] = useState<FormErrors>({});
  const [sent, setSent] = useState<(InviteUserResult & { email: string }) | null>(null);
  const submitting = useRef(false);

  const reset = () => {
    setEmail("");
    setName("");
    setRole(DEFAULT_INVITE_ROLE);
    setErrors({});
    setSent(null);
    invite.reset();
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: reset only when the sheet opens.
  useEffect(() => {
    if (open) reset();
  }, [open]);

  const submit = async (event?: FormEvent) => {
    event?.preventDefault();
    // A second Enter before isPending re-renders would send (and re-token) the invite twice.
    if (submitting.current) return;
    const trimmed = email.trim();
    if (!trimmed) {
      setErrors({ email: "Enter an email address." });
      return;
    }
    setErrors({});
    submitting.current = true;
    try {
      const result = await invite.mutateAsync({ email: trimmed, name: name.trim(), role });
      setSent({ ...result, email: trimmed.toLowerCase() });
    } catch (err) {
      setErrors(errorsFor(err));
    } finally {
      submitting.current = false;
    }
  };

  return (
    <SideSheet open={open} onClose={onClose} title="Invite user">
      {sent ? (
        <div className="flex flex-col gap-4 p-4">
          <div role="status" className="flex items-start gap-2">
            <Icon name="circle-check" size={20} className="shrink-0 text-success" aria-hidden="true" />
            <div className="flex flex-col gap-1">
              <p className="font-medium text-text">Invitation sent to {sent.email}</p>
              {sent.existingAccount && (
                <p className="text-sm text-text-secondary">
                  This person already has a GoERP account and will be added to this organisation.
                </p>
              )}
            </div>
          </div>
          <div className="flex gap-2">
            <Button variant="secondary" onClick={reset}>
              Invite another
            </Button>
            <Button variant="primary" onClick={onClose}>
              Done
            </Button>
          </div>
        </div>
      ) : (
        <form className="flex flex-col gap-4 p-4" onSubmit={(event) => void submit(event)} noValidate>
          <FieldWrapper label="Email" required error={errors.email}>
            <TextInput type="email" value={email} onChange={setEmail} autoComplete="off" />
          </FieldWrapper>
          <FieldWrapper label="Full name" description="Optional.">
            <TextInput value={name} onChange={setName} autoComplete="off" />
          </FieldWrapper>
          <FieldWrapper label="Role" required error={errors.role}>
            <Select
              options={roles}
              value={role}
              onChange={(value) => setRole(typeof value === "string" ? value : DEFAULT_INVITE_ROLE)}
            />
          </FieldWrapper>
          {errors.form && (
            <p role="alert" className="text-danger text-sm">
              {errors.form}
            </p>
          )}
          <div>
            <ActionButton variant="primary" loading={invite.isPending} onClick={() => void submit()}>
              Send invite
            </ActionButton>
          </div>
        </form>
      )}
    </SideSheet>
  );
}
