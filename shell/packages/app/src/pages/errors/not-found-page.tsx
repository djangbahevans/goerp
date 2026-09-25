import { Button } from "@goerp/sdk/components";
import { useRouter } from "@tanstack/react-router";
import { type ReactNode, useState } from "react";
import { ButtonLink } from "../../router/button-link.js";
import { ErrorLayout } from "./error-layout.js";

// shell-ux.md §6.1: the compass points somewhere different each visit.
export function NotFoundPage(): ReactNode {
  const router = useRouter();
  const [heading] = useState(() => Math.floor(Math.random() * 360));
  return (
    <ErrorLayout
      icon="compass"
      iconStyle={{ transform: `rotate(${heading}deg)` }}
      heading="Page not found"
      description="This page doesn't exist or has been moved."
      actions={
        <>
          <ButtonLink to="/" variant="primary">
            Go home
          </ButtonLink>
          <Button onClick={() => router.history.back()}>Go back</Button>
        </>
      }
    />
  );
}
