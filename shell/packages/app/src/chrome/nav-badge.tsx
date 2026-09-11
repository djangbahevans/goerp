import type { APIClient } from "@goerp/sdk";
import { apiClient } from "@goerp/sdk";
import { useQuery } from "@tanstack/react-query";
import type { CSSProperties, ReactNode } from "react";

const BADGE_CAP = 99;
const POLL_INTERVAL_MS = 30_000;
const STALE_TIME_MS = 15_000;

type CountClient = Pick<APIClient, "get">;

// Collapsed mode has no room next to a hidden label for an inline pill — it
// overlays the icon's end corner instead, same idea as a notification-dot,
// sized to sit fully inside the icon's own box (not hanging outward): the
// 56px rail has no buffer outside its own edge for an overhanging badge.
const COLLAPSED_STYLE: CSSProperties = {
  position: "absolute",
  insetInlineEnd: "2px",
  top: "2px",
  minWidth: "12px",
  height: "12px",
  fontSize: "8px",
  lineHeight: "12px",
  textAlign: "center",
  padding: "0 2px",
};

// shell-architecture.md §16's NavBadge: a live-polled unread/pending count
// for one nav item's badge_count_route, capped at "99+".
export function NavBadge({
  route,
  collapsed = false,
  client = apiClient,
}: {
  route: string;
  collapsed?: boolean;
  client?: CountClient;
}): ReactNode {
  const { data } = useQuery({
    queryKey: ["nav-badge", route],
    queryFn: () => client.get<{ count: number }>(route),
    refetchInterval: POLL_INTERVAL_MS,
    staleTime: STALE_TIME_MS,
  });

  if (!data?.count) return null;
  if (collapsed) {
    return (
      <span className="rounded-full bg-primary font-medium text-text-inverse" style={COLLAPSED_STYLE}>
        {data.count > BADGE_CAP ? `${BADGE_CAP}+` : data.count}
      </span>
    );
  }
  return (
    <span className="ms-auto rounded-full bg-primary px-1.5 py-0.5 text-[10px] font-medium text-text-inverse">
      {data.count > BADGE_CAP ? `${BADGE_CAP}+` : data.count}
    </span>
  );
}
