import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { BADGE_COLOR_CLASSES, Badge, type BadgeColor } from "./badge.js";

afterEach(cleanup);

const ALL_COLORS: BadgeColor[] = [
  "gray",
  "red",
  "orange",
  "yellow",
  "green",
  "teal",
  "blue",
  "indigo",
  "purple",
  "pink",
];

describe("Badge", () => {
  it("renders the label", () => {
    render(<Badge label="Confirmed" />);
    expect(screen.getByText("Confirmed")).toBeTruthy();
  });

  it("defaults to gray when no color is given", () => {
    render(<Badge label="Confirmed" />);
    for (const cls of BADGE_COLOR_CLASSES.gray.split(" ")) {
      expect(screen.getByText("Confirmed").className).toContain(cls);
    }
  });

  it.each(ALL_COLORS)("supports the %s BadgeColor", (color) => {
    render(<Badge label="x" color={color} />);
    for (const cls of BADGE_COLOR_CLASSES[color].split(" ")) {
      expect(screen.getByText("x").className).toContain(cls);
    }
  });

  it("falls back to gray for an unrecognized color, e.g. a mistyped manifest value", () => {
    render(<Badge label="x" color={"chartreuse" as BadgeColor} />);
    for (const cls of BADGE_COLOR_CLASSES.gray.split(" ")) {
      expect(screen.getByText("x").className).toContain(cls);
    }
  });
});
