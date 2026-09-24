import { createFileRoute } from "@tanstack/react-router";
import { RegisterPage } from "../../auth/register-page.js";

// shell-ux.md §2.2.
export const Route = createFileRoute("/auth/register")({
  component: RegisterPage,
});
