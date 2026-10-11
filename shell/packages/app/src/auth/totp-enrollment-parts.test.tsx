import { toast } from "@goerp/sdk/notifications";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TOTPScanDetails } from "./totp-enrollment-parts.js";

const enrollment = {
  enrollmentId: "enrollment-1",
  qrSvg:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 29 29"><rect width="29" height="29" fill="white"/></svg>',
  secret: "JBSWY3DPEHPK3PXP",
};

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("TOTPScanDetails", () => {
  it("uses the enrollment SVG with a grouped manual key as its visible description", () => {
    const { container } = render(<TOTPScanDetails enrollment={enrollment} />);
    const image = screen.getByRole("img", { name: "QR code for authenticator app setup" });
    const caption = document.getElementById(image.getAttribute("aria-describedby")!);

    expect(decodeURIComponent(image.getAttribute("src")!.split(",")[1]!)).toBe(enrollment.qrSvg);
    expect(container.querySelector("svg")).toBeNull();
    expect(caption?.textContent).toContain("JBSW Y3DP EHPK 3PXP");
    expect(screen.getByRole("button", { name: "Copy setup key" })).toBeTruthy();
  });

  it("copies the original secret when the supplied image fails", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    const success = vi.spyOn(toast, "success").mockImplementation(() => "toast-1");
    render(<TOTPScanDetails enrollment={enrollment} />);
    fireEvent.error(screen.getByRole("img"));
    fireEvent.click(screen.getByRole("button", { name: "Copy setup key" }));

    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.getByText("JBSW Y3DP EHPK 3PXP")).toBeTruthy();
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(enrollment.secret));
    expect(success).toHaveBeenCalledWith("Key copied.");
  });
});
