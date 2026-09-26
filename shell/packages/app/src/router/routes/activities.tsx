import { createFileRoute } from "@tanstack/react-router";
import { MyActivitiesPage } from "../../activities/my-activities-page.js";

// shell-ux.md §8.
export const Route = createFileRoute("/activities")({
  staticData: { breadcrumb: "My activities" },
  component: MyActivitiesPage,
});
