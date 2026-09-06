import { modelRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

// Shown when the model declares .Shareable(), gated off the model
// registry. The share-management panel itself is goerp#476's scope.
export function ShareHeaderAction({ resource, recordId }: { resource: string; recordId: string | undefined }) {
  const { data: model } = useQuery({
    queryKey: ["form-model-shareable", resource],
    queryFn: () => modelRegistry.resolve(resource),
  });
  const [open, setOpen] = useState(false);

  if (!model?.shareable || recordId === undefined) return null;

  return (
    <span>
      <button type="button" onClick={() => setOpen((v) => !v)}>
        Share
      </button>
      {open && <p>Share management — see goerp#476.</p>}
    </span>
  );
}
