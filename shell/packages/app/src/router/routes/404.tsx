import { createFileRoute } from "@tanstack/react-router";
import { NotFoundPage } from "../../pages/errors/index.js";

// shell-ux.md §6.1. Unmatched URLs render the same page in place, through the
// root route's notFoundComponent.
export const Route = createFileRoute("/404")({
  component: NotFoundPage,
});
