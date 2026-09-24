import { TextLink } from "@goerp/sdk/components";
import { createLink } from "@tanstack/react-router";

// docs/components/text-link.md "Client-side routing": a text link that
// navigates client-side.
export const RouterTextLink = createLink(TextLink);
