import { describe, expect, it } from "vitest";
import { declarationElements, fieldSecurityDecision } from "./view-debug-model.js";

describe("declarationElements", () => {
  it("lists each identifiable array entry with its path, nested ones included", () => {
    const elements = declarationElements({
      name: "contact_form",
      type: "form",
      sections: [{ label: "Details", fields: [{ field: "name" }, { field: "email" }] }],
      tabs: [{ label: "Notes", content: { type: "view", view: "notes_list" } }],
      actions: [{ action: "archive", type: "route" }, {}],
    });

    expect(elements).toEqual([
      { path: "sections[0]", label: 'label "Details"' },
      { path: "sections[0].fields[0]", label: 'field "name"' },
      { path: "sections[0].fields[1]", label: 'field "email"' },
      { path: "tabs[0]", label: 'label "Notes"' },
      { path: "actions[0]", label: 'action "archive"' },
    ]);
  });
});

describe("fieldSecurityDecision", () => {
  it("shows an unrestricted or readable field", () => {
    expect(fieldSecurityDecision(undefined, false)).toEqual({ decision: "shown", detail: "no field access rule" });
    expect(fieldSecurityDecision({ read: true, write: false }, false)).toEqual({
      decision: "shown",
      detail: "read only",
    });
  });

  it("reports the server's denied-read behaviour for an unreadable field", () => {
    expect(fieldSecurityDecision({ read: false, write: false, denied_read: "mask" }, false)).toEqual({
      decision: "masked",
      detail: "no read access, so the shell doesn't render it; responses return a masked value",
    });
    expect(fieldSecurityDecision({ read: false, write: false, denied_read: "nullify" }, false)).toEqual({
      decision: "hidden",
      detail: "no read access, so the shell doesn't render it; responses return null",
    });
    expect(fieldSecurityDecision({ read: false, write: false }, false).detail).toBe(
      "no read access, so the shell doesn't render it; responses omit the field",
    );
  });

  it("hides every field while permissions are pending", () => {
    expect(fieldSecurityDecision(undefined, true)).toEqual({
      decision: "hidden",
      detail: "permissions have not loaded",
    });
  });
});
