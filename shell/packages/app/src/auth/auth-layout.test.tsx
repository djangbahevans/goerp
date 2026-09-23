import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AuthLayout } from "./auth-layout.js";

afterEach(cleanup);

describe("AuthLayout", () => {
  it("renders its children inside the page's single main landmark", () => {
    render(
      <AuthLayout>
        <h1>Sign in</h1>
      </AuthLayout>,
    );
    const mains = screen.getAllByRole("main");
    expect(mains).toHaveLength(1);
    expect(mains[0]?.contains(screen.getByRole("heading", { level: 1, name: "Sign in" }))).toBe(true);
  });

  it("renders no chrome landmarks", () => {
    render(
      <AuthLayout privacyPolicyUrl="https://example.com/privacy">
        <h1>Sign in</h1>
      </AuthLayout>,
    );
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(screen.queryByRole("banner")).toBeNull();
    expect(screen.queryByRole("complementary")).toBeNull();
  });

  it("renders the tenant logo with its alt text when given", () => {
    render(
      <AuthLayout tenantLogo={{ url: "/logo.svg", alt: "Acme Corp" }}>
        <h1>Sign in</h1>
      </AuthLayout>,
    );
    const logo = screen.getByRole("img", { name: "Acme Corp" });
    expect(logo.getAttribute("src")).toBe("/logo.svg");
  });

  it("renders no image when the tenant logo is omitted", () => {
    render(
      <AuthLayout>
        <h1>Sign in</h1>
      </AuthLayout>,
    );
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("always renders the Powered by GoERP footer text", () => {
    render(
      <AuthLayout>
        <h1>Sign in</h1>
      </AuthLayout>,
    );
    expect(screen.getByText("Powered by GoERP")).toBeTruthy();
  });

  it("renders the privacy policy link after the card content when a URL is given", () => {
    render(
      <AuthLayout privacyPolicyUrl="https://example.com/privacy">
        <button type="button">Continue</button>
      </AuthLayout>,
    );
    const link = screen.getByRole("link", { name: "Privacy policy" });
    expect(link.getAttribute("href")).toBe("https://example.com/privacy");
    const button = screen.getByRole("button", { name: "Continue" });
    expect(button.compareDocumentPosition(link) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("omits the privacy policy link when no URL is given", () => {
    render(
      <AuthLayout>
        <h1>Sign in</h1>
      </AuthLayout>,
    );
    expect(screen.queryByRole("link")).toBeNull();
  });
});
