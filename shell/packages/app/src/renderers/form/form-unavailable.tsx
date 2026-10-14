import { ActionButton, Button, PageHeader, TextArea } from "@goerp/sdk/components";
import { type RefObject, useEffect, useId } from "react";
import type { Row } from "../list/list-view-types.js";

export function FormUnavailable({
  label,
  edits,
  onDiscard,
  heading,
}: {
  label: string;
  edits: Row;
  onDiscard: () => void;
  heading: RefObject<HTMLHeadingElement | null>;
}) {
  const draftId = useId();
  const hasEdits = Object.keys(edits).length > 0;

  useEffect(() => {
    heading.current?.focus();
  }, [heading]);

  return (
    <>
      <PageHeader title={label} actions={<Button disabled>Save</Button>} />
      <section className="space-y-4">
        <h2 ref={heading} tabIndex={-1} className="font-medium text-lg text-text">
          This record is no longer available
        </h2>
        {hasEdits && (
          <>
            <p className="text-sm text-text-secondary">Copy your unsaved changes before leaving this page.</p>
            <div className="space-y-2">
              <label htmlFor={draftId} className="font-medium text-sm text-text">
                Unsaved changes
              </label>
              <TextArea id={draftId} readOnly rows={8} value={JSON.stringify(edits, null, 2)} onChange={() => {}} />
            </div>
            <ActionButton variant="secondary" onClick={onDiscard}>
              Discard changes
            </ActionButton>
          </>
        )}
      </section>
    </>
  );
}
