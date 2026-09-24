import { createFileRoute } from "@tanstack/react-router";
import { ForgotPasswordPage } from "../../auth/forgot-password-page.js";

// shell-ux.md §2.3.
export const Route = createFileRoute("/auth/forgot-password")({
  component: ForgotPasswordPage,
});
