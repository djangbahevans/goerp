import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ErrorBoundary } from "./error-boundary.js";

afterEach(cleanup);

function Thrower(): never {
  throw new Error("boom");
}

function StringThrower(): never {
  throw "raw string thrown";
}

describe("ErrorBoundary", () => {
  beforeEach(() => {
    // React logs the caught error to console.error; silence it so the test output stays clean.
    vi.spyOn(console, "error").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders children when nothing throws", () => {
    render(
      <ErrorBoundary fallback={() => <p>fallback</p>}>
        <p>content</p>
      </ErrorBoundary>,
    );
    expect(screen.getByText("content")).toBeTruthy();
    expect(screen.queryByText("fallback")).toBeNull();
  });

  it("renders fallback with the caught error when a descendant throws during render", () => {
    render(
      <ErrorBoundary fallback={(error) => <p>Failed: {error.message}</p>}>
        <Thrower />
      </ErrorBoundary>,
    );
    expect(screen.getByText("Failed: boom")).toBeTruthy();
  });

  it("normalizes a non-Error thrown value into an Error before passing it to fallback", () => {
    render(
      <ErrorBoundary fallback={(error) => <p>Failed: {error.message}</p>}>
        <StringThrower />
      </ErrorBoundary>,
    );
    expect(screen.getByText("Failed: raw string thrown")).toBeTruthy();
  });

  it("announces the fallback content via an alert role", () => {
    render(
      <ErrorBoundary fallback={(error) => <p>Failed: {error.message}</p>}>
        <Thrower />
      </ErrorBoundary>,
    );
    expect(screen.getByRole("alert").textContent).toBe("Failed: boom");
  });

  it("re-renders children after reset is called", () => {
    let shouldThrow = true;
    function MaybeThrow(): ReactNode {
      if (shouldThrow) throw new Error("boom");
      return <p>recovered</p>;
    }

    render(
      <ErrorBoundary
        fallback={(_error, reset) => (
          <button type="button" onClick={reset}>
            Retry
          </button>
        )}
      >
        <MaybeThrow />
      </ErrorBoundary>,
    );

    shouldThrow = false;
    fireEvent.click(screen.getByText("Retry"));
    expect(screen.getByText("recovered")).toBeTruthy();
  });
});
