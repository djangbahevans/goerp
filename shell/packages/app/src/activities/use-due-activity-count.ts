import { useAuth } from "@goerp/sdk/auth";
import { useMyActivities } from "@goerp/sdk/react";
import { useEffect } from "react";
import { todayIn } from "./activity-dates.js";

// The page size shell-ux.md §8's page requests; the badge shares its query
// so marking an activity done on the page updates both at once.
export const MY_ACTIVITIES_PAGE_SIZE = 50;

// The caller's timezone, resolved like a request's (l10n-guide.md §2): their
// own, then the tenant default.
export function useActivityTimezone(): string {
  const { user, tenant } = useAuth();
  return user?.timezone || tenant?.defaultTimezone || "UTC";
}

// The count of overdue plus due-today activities for the user menu's badge
// (chrome-header.md). "Mine" is sorted soonest due first, so pages load only
// until one ends past today; the count is exact without loading the rest.
export function useDueActivityCount(): number {
  const timeZone = useActivityTimezone();
  const { activities, hasMore, fetchMore, isFetchingNextPage, isError } = useMyActivities({
    limit: MY_ACTIVITIES_PAGE_SIZE,
  });
  const today = todayIn(timeZone);
  const last = activities.at(-1);
  const needMore = hasMore && !isFetchingNextPage && !isError && (last === undefined || last.dueDate <= today);

  useEffect(() => {
    if (needMore) fetchMore();
  }, [needMore, fetchMore]);

  return activities.filter((a) => a.dueDate <= today).length;
}
