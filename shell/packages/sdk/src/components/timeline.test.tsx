import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Timeline, TimelineItem } from "./timeline.js";

afterEach(cleanup);

describe("Timeline", () => {
  it("renders title, description, and timestamp for each item", () => {
    render(
      <Timeline>
        <TimelineItem
          title="Order confirmed"
          description="Order #1024 was confirmed"
          timestamp="2026-01-15T10:30:00.000Z"
        />
      </Timeline>,
    );
    expect(screen.getByText("Order confirmed")).toBeTruthy();
    expect(screen.getByText("Order #1024 was confirmed")).toBeTruthy();
  });

  it("sets the machine-readable dateTime attribute from the timestamp", () => {
    render(
      <Timeline>
        <TimelineItem title="Order confirmed" timestamp="2026-01-15T10:30:00.000Z" />
      </Timeline>,
    );
    const time = document.querySelector("time");
    expect(time?.getAttribute("dateTime")).toBe("2026-01-15T10:30:00.000Z");
  });

  it("renders user attribution when provided", () => {
    render(
      <Timeline>
        <TimelineItem title="Order confirmed" timestamp="2026-01-15T10:30:00.000Z" user={{ name: "Ama Boateng" }} />
      </Timeline>,
    );
    expect(screen.getAllByText("Ama Boateng").length).toBeGreaterThan(0);
  });

  it("omits user attribution when not provided", () => {
    render(
      <Timeline>
        <TimelineItem title="Order confirmed" timestamp="2026-01-15T10:30:00.000Z" />
      </Timeline>,
    );
    expect(screen.queryByText("Ama Boateng")).toBeNull();
  });

  it("renders a custom icon", () => {
    render(
      <Timeline>
        <TimelineItem title="Order confirmed" timestamp="2026-01-15T10:30:00.000Z" icon={<span>📦</span>} />
      </Timeline>,
    );
    expect(screen.getByText("📦")).toBeTruthy();
  });
});
