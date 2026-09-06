import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ShareHeaderAction } from "./form-share-action.js";

const { resolveModelMock } = vi.hoisted(() => ({ resolveModelMock: vi.fn() }));
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, modelRegistry: { resolve: resolveModelMock } };
});

afterEach(() => {
  cleanup();
  resolveModelMock.mockReset();
});

function renderShare(recordId: string | undefined) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ShareHeaderAction resource="contacts.contact" recordId={recordId} />
    </QueryClientProvider>,
  );
}

describe("ShareHeaderAction", () => {
  it("shows nothing for a non-Shareable model", async () => {
    resolveModelMock.mockResolvedValue({ shareable: false });
    renderShare("01j");
    await vi.waitFor(() => expect(resolveModelMock).toHaveBeenCalled());
    expect(screen.queryByText("Share")).toBeNull();
  });

  it("shows nothing on a create form (no recordId yet), even for a Shareable model", async () => {
    resolveModelMock.mockResolvedValue({ shareable: true });
    renderShare(undefined);
    await vi.waitFor(() => expect(resolveModelMock).toHaveBeenCalled());
    expect(screen.queryByText("Share")).toBeNull();
  });

  it("shows the Share button for a Shareable model with an open record, and opens the panel placeholder on click", async () => {
    resolveModelMock.mockResolvedValue({ shareable: true });
    renderShare("01j");
    const button = await screen.findByText("Share");
    fireEvent.click(button);
    expect(screen.getByText(/goerp#476/)).toBeTruthy();
  });
});
