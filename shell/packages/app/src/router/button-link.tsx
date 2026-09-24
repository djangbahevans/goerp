import { Button } from "@goerp/sdk/components";
import { createLink } from "@tanstack/react-router";

// docs/components/button.md "Navigation": a button-styled link that
// navigates client-side.
export const ButtonLink = createLink(Button);
