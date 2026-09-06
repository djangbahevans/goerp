import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { UserAvatar } from "./user-avatar.js";

afterEach(cleanup);

describe("UserAvatar", () => {
  it("renders an image when avatarUrl is given", () => {
    render(<UserAvatar name="Ama Owusu" avatarUrl="https://example.com/ama.png" />);
    const img = screen.getByRole("img", { name: "Ama Owusu" });
    expect(img.getAttribute("src")).toBe("https://example.com/ama.png");
  });

  it("falls back to initials when avatarUrl is null", () => {
    render(<UserAvatar name="Ama Owusu" avatarUrl={null} />);
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.getByText("AO")).toBeTruthy();
  });

  it("falls back to initials when avatarUrl is omitted", () => {
    render(<UserAvatar name="Kwame Mensah" />);
    expect(screen.getByText("KM")).toBeTruthy();
  });

  it.each(["xs", "sm", "md", "lg"] as const)("supports the %s size", (size) => {
    render(<UserAvatar name="Ama Owusu" size={size} />);
    expect(screen.getByText("AO")).toBeTruthy();
  });

  it("shows a tooltip only when showTooltip is set", () => {
    render(<UserAvatar name="Ama Owusu" showTooltip />);
    expect(screen.getByText("AO").getAttribute("title")).toBe("Ama Owusu");

    cleanup();
    render(<UserAvatar name="Ama Owusu" />);
    expect(screen.getByText("AO").getAttribute("title")).toBeNull();
  });
});
