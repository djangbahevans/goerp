import { cleanup, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TextLink } from "./text-link.js";

afterEach(cleanup);

describe("TextLink", () => {
  it("standalone: underlined only on hover and kept on one line", () => {
    render(<TextLink href="/auth/register">Create an account</TextLink>);
    const link = screen.getByRole("link", { name: "Create an account" });
    expect(link.getAttribute("href")).toBe("/auth/register");
    expect(link.className).toContain("hover:underline");
    expect(link.className).toContain("whitespace-nowrap");
    expect(link.className.split(" ")).not.toContain("underline");
  });

  it("inline: always underlined, thickening on hover", () => {
    render(
      <TextLink href="/terms" inline>
        terms of service
      </TextLink>,
    );
    const classes = screen.getByRole("link").className.split(" ");
    expect(classes).toContain("underline");
    expect(classes).toContain("hover:decoration-2");
  });

  it("external: opens a new tab and says so to screen readers", () => {
    render(
      <TextLink href="https://example.com/terms" external>
        terms of service
      </TextLink>,
    );
    const link = screen.getByRole("link");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    expect(screen.getByText("(opens in a new tab)").className).toContain("sr-only");
    expect(link.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("forwards ref and anchor attributes, and drops className", () => {
    const ref = createRef<HTMLAnchorElement>();
    const onClick = vi.fn();
    const props = { className: "text-danger" } as object;
    render(
      <TextLink ref={ref} href="/" aria-current="page" data-status="active" onClick={onClick} {...props}>
        Home
      </TextLink>,
    );
    const link = screen.getByRole("link");
    expect(ref.current).toBe(link);
    expect(link.getAttribute("aria-current")).toBe("page");
    expect(link.dataset.status).toBe("active");
    expect(link.className).not.toContain("text-danger");
    link.click();
    expect(onClick).toHaveBeenCalled();
  });
});
