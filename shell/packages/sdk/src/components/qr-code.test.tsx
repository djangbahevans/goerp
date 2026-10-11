import { cleanup, render, screen } from "@testing-library/react";
import jsQR from "jsqr";
import { afterEach, describe, expect, it } from "vitest";
import { QRCode } from "./qr-code.js";

afterEach(cleanup);

function decodeRenderedCode(): string | undefined {
  const svg = screen.getByRole("img");
  const size = Number(svg.getAttribute("viewBox")?.split(" ")[2]);
  const scale = 4;
  const width = size * scale;
  const pixels = new Uint8ClampedArray(width * width * 4).fill(255);
  const path = svg.querySelector("path")?.getAttribute("d") ?? "";

  for (const match of path.matchAll(/M(\d+) (\d+)h1v1h-1z/g)) {
    const column = Number(match[1]);
    const row = Number(match[2]);
    expect(column).toBeGreaterThanOrEqual(4);
    expect(row).toBeGreaterThanOrEqual(4);
    expect(column).toBeLessThan(size - 4);
    expect(row).toBeLessThan(size - 4);

    for (let y = row * scale; y < (row + 1) * scale; y++) {
      for (let x = column * scale; x < (column + 1) * scale; x++) {
        const offset = (y * width + x) * 4;
        pixels[offset] = 0;
        pixels[offset + 1] = 0;
        pixels[offset + 2] = 0;
      }
    }
  }

  return jsQR(pixels, width, width)?.data;
}

describe("QRCode", () => {
  it.each([
    "https://example.test/records/123?token=a%2Fb#details",
    "Akwaaba — 中文 😀 e\u0301",
    "  first line\nsecond line\tend  ",
    " ",
  ])("renders a code that independently decodes to the exact payload %j", (value) => {
    const { container } = render(<QRCode value={value} label="Record" />);

    expect(decodeRenderedCode()).toBe(value);
    expect(container.querySelector("figcaption")?.textContent).toBe(value);
    expect(screen.getByRole("img", { name: "QR code for Record" })).toBeTruthy();
    expect(container.querySelector("rect")?.getAttribute("fill")).toBe("#FFFFFF");
    expect(container.querySelector("path")?.getAttribute("fill")).toBe("#000000");
  });

  it("provides a unique visible text description for each image", () => {
    render(
      <>
        <QRCode value="one" />
        <QRCode value="two" />
      </>,
    );

    const images = screen.getAllByRole("img", { name: "QR code" });
    const ids = images.map((image) => image.getAttribute("aria-describedby"));
    expect(new Set(ids).size).toBe(2);
    expect(ids.map((id) => document.getElementById(id!)?.textContent)).toEqual(["one", "two"]);
    expect(document.querySelector("input, textarea, button, [tabindex]")).toBeNull();
  });

  it("shows an accessible empty state without an image", () => {
    render(<QRCode value="" />);

    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.getByText("—").getAttribute("aria-hidden")).toBe("true");
    expect(screen.getByText("No value").className).toContain("sr-only");
  });

  it.each(["x".repeat(2400), "1".repeat(6000), "invalid\uD800", "invalid\uDC00"])(
    "retains an unencodable payload without rendering a substituted code",
    (value) => {
      const { container } = render(<QRCode value={value} />);

      expect(screen.queryByRole("img")).toBeNull();
      expect(screen.getByText("This value cannot be displayed as a QR code.")).toBeTruthy();
      expect(container.querySelector("figcaption")?.textContent).toBe(value);
    },
  );

  it("updates the code and recovers from empty and unencodable values", () => {
    const { rerender } = render(<QRCode value="first" />);
    expect(decodeRenderedCode()).toBe("first");

    rerender(<QRCode value={"x".repeat(2400)} />);
    expect(screen.queryByRole("img")).toBeNull();

    rerender(<QRCode value="" />);
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.queryByText("This value cannot be displayed as a QR code.")).toBeNull();

    rerender(<QRCode value="second 😀" />);
    expect(decodeRenderedCode()).toBe("second 😀");
  });

  it("renders markup as literal text", () => {
    const value = '<img src="https://example.test" onerror="alert(1)">';
    const { container } = render(<QRCode value={value} />);

    expect(container.querySelector("img, script")).toBeNull();
    expect(container.querySelector("figcaption")?.textContent).toBe(value);
    expect(decodeRenderedCode()).toBe(value);
  });
});
