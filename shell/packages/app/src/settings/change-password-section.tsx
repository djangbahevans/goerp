import { useAuth } from "@goerp/sdk/auth";
import { Button, PasswordField, PasswordStrengthMeter, SectionCard } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { useLocation } from "@tanstack/react-router";
import { type ReactNode, type SubmitEvent, useEffect, useRef, useState } from "react";
import { PASSWORD_MISMATCH, policyMessageAsSentence } from "../auth/password-messages.js";

// The #change-password anchor the password update banner links to
// (shell-ux.md §2.1, §4.1).
export const CHANGE_PASSWORD_ANCHOR = "change-password";

interface FieldErrors {
  current?: string | undefined;
  next?: string | undefined;
  confirm?: string | undefined;
}

// shell-ux.md §4.1 "Change password section". Independent of the profile
// form's "Save changes": it's a separate request that signs out every other
// session.
export function ChangePasswordSection(): ReactNode {
  const { changePassword } = useAuth();
  const hash = useLocation({ select: (location) => location.hash });
  const sectionRef = useRef<HTMLDivElement>(null);

  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (hash !== CHANGE_PASSWORD_ANCHOR) return;
    const section = sectionRef.current;
    if (!section) return;
    section.scrollIntoView?.({ block: "start" });
    section.querySelector<HTMLInputElement>('input[autocomplete="current-password"]')?.focus();
  }, [hash]);

  const handleConfirmBlur = () => {
    setErrors((e) => ({ ...e, confirm: confirm && confirm !== next ? PASSWORD_MISMATCH : undefined }));
  };

  const handleSubmit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submitting) return;

    const found: FieldErrors = {};
    if (!current) found.current = "Enter your current password.";
    if (!next) found.next = "Enter a new password.";
    if (!confirm) found.confirm = "Confirm your new password.";
    else if (confirm !== next) found.confirm = PASSWORD_MISMATCH;
    setErrors(found);
    if (Object.keys(found).length > 0) return;

    setSubmitting(true);
    try {
      await changePassword({ currentPassword: current, newPassword: next });
      setCurrent("");
      setNext("");
      setConfirm("");
      toast.success("Password updated. Other sessions were signed out.");
    } catch (err) {
      if (isAppError(err) && err.code === "invalid_password") {
        setCurrent("");
        setErrors({ current: "Current password is incorrect." });
      } else if (isAppError(err) && err.code === "auth.password_too_weak") {
        setErrors({ next: policyMessageAsSentence(err.message) });
      } else {
        toast.error("Couldn't change your password. Try again.");
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div id={CHANGE_PASSWORD_ANCHOR} ref={sectionRef} className="scroll-mt-(--space-4)">
      <SectionCard title="Change password">
        <form noValidate onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-4">
          <PasswordField
            label="Current password"
            autoComplete="current-password"
            value={current}
            onChange={setCurrent}
            error={errors.current}
            disabled={submitting}
          />
          <div className="flex flex-col gap-2">
            <PasswordField
              label="New password"
              autoComplete="new-password"
              value={next}
              onChange={setNext}
              error={errors.next}
              disabled={submitting}
            />
            {/* Guidance only: the server's tenant policy decides. */}
            <PasswordStrengthMeter password={next} />
          </div>
          <PasswordField
            label="Confirm new password"
            autoComplete="new-password"
            value={confirm}
            onChange={setConfirm}
            onBlur={handleConfirmBlur}
            error={errors.confirm}
            disabled={submitting}
          />
          <div>
            <Button type="submit" loading={submitting}>
              Change password
            </Button>
          </div>
        </form>
      </SectionCard>
    </div>
  );
}
