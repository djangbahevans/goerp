import { createFileRoute, notFound } from "@tanstack/react-router";
import { NotFoundPage } from "../../pages/errors/index.js";

// Keeps every /admin/* URL under the admin layout, so its role gate runs
// before an unknown admin page 404s.
export const Route = createFileRoute("/admin/$")({
  beforeLoad: () => {
    throw notFound();
  },
  notFoundComponent: NotFoundPage,
});
