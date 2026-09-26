import { describe, expect, it } from "vitest";
import { handoffURL } from "./handoff-url.js";

describe("handoffURL", () => {
  it("keeps the current page's scheme and port on the tenant's host", () => {
    expect(handoffURL({ host: "acme.localhost", code: "c0de" }, "/", { protocol: "http:", port: "5173" })).toBe(
      "http://acme.localhost:5173/auth/handoff?code=c0de",
    );
  });

  it("omits an empty port and carries a non-default redirect", () => {
    expect(
      handoffURL({ host: "acme.goerp.io", code: "c0de" }, "/_m/crm/contacts", { protocol: "https:", port: "" }),
    ).toBe("https://acme.goerp.io/auth/handoff?code=c0de&redirect=%2F_m%2Fcrm%2Fcontacts");
  });
});
