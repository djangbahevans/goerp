import { PageHeader, PageLayout } from "@goerp/sdk/components";
import type { ReactNode, Ref } from "react";
import { useLayoutEffect, useState } from "react";

// list-renderer.md's page composition, shared by the list, kanban,
// calendar, pivot and timeline renderers.

// shell-architecture.md §20: a full-page render owns its page chrome; an
// embedded one leaves it to the parent form.
export function ViewPage({
  embedded,
  title,
  actions,
  children,
}: {
  embedded: boolean | undefined;
  title: string;
  actions?: ReactNode;
  children: ReactNode;
}): ReactNode {
  if (embedded) return children;
  return (
    <PageLayout>
      <PageHeader title={title} {...(actions !== undefined ? { actions } : {})} />
      {children}
    </PageLayout>
  );
}

export function ViewSurface({ children }: { children: ReactNode }): ReactNode {
  return <div className="rounded-structural border border-border bg-surface">{children}</div>;
}

// Hidden when neither region renders a control: ListFilters and the
// controls can each come back empty after condition/permission checks.
const HIDE_WHEN_NO_CONTROLS = "[&:not(:has(button,input,a))]:hidden";

// Tailwind's @2xl container breakpoint, which the controls cluster's classes below key off.
const NARROW_TOOLBAR_REM = 42;

function contentWidth(element: HTMLElement): number {
  const style = getComputedStyle(element);
  return (
    element.clientWidth - (Number.parseFloat(style.paddingLeft) || 0) - (Number.parseFloat(style.paddingRight) || 0)
  );
}

// For a caller that renders different controls in a narrow toolbar, which CSS alone can't unmount.
export function useNarrowToolbar(): {
  ref: (element: HTMLDivElement | null) => void;
  element: HTMLDivElement | null;
  narrow: boolean;
} {
  const [element, setElement] = useState<HTMLDivElement | null>(null);
  const [narrow, setNarrow] = useState(false);
  useLayoutEffect(() => {
    if (!element) return;
    const measure = () => {
      const width = contentWidth(element);
      // A toolbar that isn't laid out (a hidden tab panel, jsdom) measures 0: keep the last layout.
      if (width <= 0) return;
      const rem = Number.parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
      setNarrow(width < NARROW_TOOLBAR_REM * rem);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [element]);
  return { ref: setElement, element, narrow };
}

export function ViewToolbar({
  filters,
  controls,
  standalone = false,
  ref,
}: {
  filters: ReactNode;
  controls?: ReactNode;
  // Kanban: a bar of its own above the board, not the top of a surface.
  standalone?: boolean;
  ref?: Ref<HTMLDivElement>;
}): ReactNode {
  const frame = standalone ? "rounded-structural border border-border bg-surface" : "border-border border-b";
  return (
    <div
      ref={ref}
      className={`@container flex flex-wrap items-end justify-between gap-4 p-3 ${frame} ${HIDE_WHEN_NO_CONTROLS}`}
    >
      {filters}
      {controls !== undefined && (
        // Keyed to the toolbar's own width so an embedded list stacks too; right-justified
        // wrapping in a narrow toolbar would leave every wrapped row hanging off the right edge.
        <div
          data-view-controls=""
          className="flex w-full flex-wrap items-end gap-2 *:grow @2xl:ms-auto @2xl:w-auto @2xl:justify-end @2xl:*:grow-0"
        >
          {controls}
        </div>
      )}
    </div>
  );
}
