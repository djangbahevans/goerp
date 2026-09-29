import {
  Button,
  EscapeLayer,
  Skeleton,
  UserAvatar,
  useFloatingPanelLayer,
  useFloatingPanelPosition,
  useOutsideClickClose,
} from "@goerp/sdk/components";
import type { UseRecordFollowersResult } from "@goerp/sdk/react";
import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";

// docs/components/form-chatter.md "Follow bar": the Followers popover
// trigger and the Follow / Unfollow button.
export interface ChatterFollowBarProps {
  followers: UseRecordFollowersResult;
  viewerId: string | undefined;
  announce: (text: string) => void;
}

export function ChatterFollowBar({ followers, viewerId, announce }: ChatterFollowBarProps): ReactNode {
  const [failed, setFailed] = useState(false);
  const errorId = useId();

  // Following is optional: a failed load hides the bar rather than
  // raising an error above the comments that still work.
  if (followers.isError) return null;

  async function toggle(): Promise<void> {
    const following = followers.isFollowing;
    setFailed(false);
    try {
      await (following ? followers.unfollow() : followers.follow());
      announce(following ? "You've unfollowed this record." : "You're following this record.");
    } catch {
      setFailed(true);
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between gap-2">
        <FollowersPopover followers={followers} viewerId={viewerId} />
        {followers.isFollowing ? (
          <Button
            variant="ghost"
            size="sm"
            icon="bell-off"
            loading={followers.isUpdating}
            aria-describedby={failed ? errorId : undefined}
            onClick={() => void toggle()}
          >
            Unfollow
          </Button>
        ) : (
          <Button
            variant="secondary"
            size="sm"
            icon="bell"
            loading={followers.isUpdating}
            disabled={followers.isLoading}
            aria-describedby={failed ? errorId : undefined}
            onClick={() => void toggle()}
          >
            Follow
          </Button>
        )}
      </div>
      {failed && (
        <p id={errorId} role="alert" className="mt-1 text-right text-danger text-xs">
          Couldn't update following. Try again.
        </p>
      )}
    </div>
  );
}

function FollowersPopover({
  followers,
  viewerId,
}: {
  followers: UseRecordFollowersResult;
  viewerId: string | undefined;
}): ReactNode {
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const headingId = `${panelId}-heading`;
  const triggerRef = useRef<HTMLDivElement | null>(null);
  const buttonRef = useRef<HTMLButtonElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const headingRef = useRef<HTMLHeadingElement | null>(null);
  // Portaled, since the chatter's SectionCard clips its content.
  const position = useFloatingPanelPosition(open, triggerRef, panelRef, false);
  const layerClassName = useFloatingPanelLayer(triggerRef);
  useOutsideClickClose(open, [triggerRef, panelRef], () => setOpen(false));

  useEffect(() => {
    if (open && position) headingRef.current?.focus();
  }, [open, position]);

  function closeAndRefocus(): void {
    setOpen(false);
    buttonRef.current?.focus();
  }

  const count = followers.isLoading ? "" : ` · ${followers.followers.length}`;

  return (
    <div ref={triggerRef} className="inline-block">
      <Button
        ref={buttonRef}
        variant="ghost"
        size="sm"
        icon="users"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={() => setOpen((v) => !v)}
      >
        {`Followers${count}`}
      </Button>
      {open &&
        createPortal(
          <EscapeLayer onEscape={closeAndRefocus}>
            <div
              ref={panelRef}
              id={panelId}
              role="dialog"
              aria-labelledby={headingId}
              style={
                position
                  ? { position: "fixed", top: position.top, left: position.left }
                  : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
              }
              className={`${layerClassName} w-70 max-w-[calc(100vw-1rem)] rounded-structural border border-border bg-surface p-3 shadow-md`}
            >
              <h3
                ref={headingRef}
                id={headingId}
                tabIndex={-1}
                className="mb-2 font-semibold text-sm text-text focus:outline-none"
              >
                Followers
              </h3>
              <FollowerList followers={followers} viewerId={viewerId} />
            </div>
          </EscapeLayer>,
          document.body,
        )}
    </div>
  );
}

function FollowerList({
  followers,
  viewerId,
}: {
  followers: UseRecordFollowersResult;
  viewerId: string | undefined;
}): ReactNode {
  if (followers.isLoading) return <Skeleton lines={2} />;
  if (followers.followers.length === 0) {
    return <p className="text-sm text-text-secondary">No one follows this record yet.</p>;
  }
  return (
    <ul className="max-h-60 space-y-2 overflow-y-auto">
      {followers.followers.map(({ user }) => {
        const name = user.name ?? "Unknown user";
        return (
          <li key={user.id} className="flex min-w-0 items-center gap-2">
            <UserAvatar userId={user.id} name={name} avatarUrl={user.avatarUrl} size="xs" />
            <span className="min-w-0 truncate text-sm text-text">
              {name}
              {user.id === viewerId && <span className="text-text-secondary"> (you)</span>}
            </span>
          </li>
        );
      })}
    </ul>
  );
}
