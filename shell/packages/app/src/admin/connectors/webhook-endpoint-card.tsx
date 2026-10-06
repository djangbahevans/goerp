import { ActionButton, AlertDialog, Button, SectionCard } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useState } from "react";
import { useRevokeWebhookEndpoint } from "./admin-connectors-api.js";

async function copyUrl(url: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(url);
    toast.success("Webhook URL copied.");
  } catch {
    toast.error("Couldn't copy the webhook URL. Select it and copy it manually.");
  }
}

// The URL to register in the provider's dashboard (connector-guide.md §3).
export function WebhookEndpointCard({ name, path }: { name: string; path: string }): ReactNode {
  const revoke = useRevokeWebhookEndpoint(name);
  const [confirming, setConfirming] = useState(false);
  const url = `${window.location.origin}${path}`;

  const confirmRevoke = () => {
    setConfirming(false);
    revoke.mutate(undefined, {
      onSuccess: () => toast.success("Webhook URL revoked. Saving the configuration creates a new one."),
      onError: () => toast.error("Couldn't revoke the webhook URL."),
    });
  };

  return (
    <SectionCard title="Webhook URL">
      <div className="mt-4 flex max-w-xl flex-col gap-3">
        <p className="text-sm text-text-secondary">
          Enter this address as the webhook destination in the provider's dashboard.
        </p>
        <div className="flex items-center gap-3">
          <output
            aria-label="Webhook URL"
            className="min-w-0 flex-1 truncate rounded-control border border-border bg-bg-subtle px-3 py-2 font-mono text-sm text-text"
          >
            {url}
          </output>
          <Button variant="secondary" onClick={() => void copyUrl(url)}>
            Copy
          </Button>
          <ActionButton variant="secondary" onClick={() => setConfirming(true)} loading={revoke.isPending}>
            Revoke
          </ActionButton>
        </div>
      </div>
      <AlertDialog
        open={confirming}
        title="Revoke the webhook URL?"
        description="The provider's deliveries to this address are rejected until a new URL is created and registered with it."
        tone="warning"
        confirmLabel="Revoke"
        onCancel={() => setConfirming(false)}
        onConfirm={confirmRevoke}
      />
    </SectionCard>
  );
}
