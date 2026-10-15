import { toast } from "@goerp/sdk/notifications";
import type { ResolvedView } from "@goerp/sdk/schema";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useReportUnsavedChanges } from "../renderers/form/unsaved-changes.js";
import { DeferViewWhileUnsaved, VIEW_UPDATE_DEFERRED_MESSAGE } from "./defer-view-while-unsaved.js";

function formView(label: string, recordId = "c1"): ResolvedView {
  return {
    module: "crm",
    viewName: "contact_form",
    viewType: "form",
    declaration: { name: "contact_form", type: "form", resource: "crm.contact", label },
    permissions: [],
    bundleUrl: null,
    recordId,
  };
}

function FakeForm({ view }: { view: ResolvedView }) {
  const [dirty, setDirty] = useState(false);
  useReportUnsavedChanges(dirty);
  return (
    <>
      <h1>{view.declaration.label}</h1>
      <input type="checkbox" aria-label="Unsaved" checked={dirty} onChange={(e) => setDirty(e.target.checked)} />
    </>
  );
}

const setDirty = (dirty: boolean) => {
  const box = screen.getByRole("checkbox", { name: "Unsaved" }) as HTMLInputElement;
  if (box.checked !== dirty) fireEvent.click(box);
};

function renderPage(initial: ResolvedView) {
  const page = (view: ResolvedView) => (
    <DeferViewWhileUnsaved view={view}>{(shown) => <FakeForm view={shown} />}</DeferViewWhileUnsaved>
  );
  const rendered = render(page(initial));
  return (next: ResolvedView) => rendered.rerender(page(next));
}

const heading = () => screen.getByRole("heading").textContent;

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("DeferViewWhileUnsaved", () => {
  it("renders an updated declaration at once while nothing is unsaved", () => {
    const info = vi.spyOn(toast, "info");
    const update = renderPage(formView("Contact"));
    update(formView("Person"));
    expect(heading()).toBe("Person");
    expect(info).not.toHaveBeenCalled();
  });

  it("holds the update while the form is dirty, then applies it once the form is clean", () => {
    const info = vi.spyOn(toast, "info");
    const update = renderPage(formView("Contact"));
    setDirty(true);

    update(formView("Person"));
    expect(heading()).toBe("Contact");
    expect(info).toHaveBeenCalledExactlyOnceWith(VIEW_UPDATE_DEFERRED_MESSAGE);

    update(formView("Person"));
    expect(info).toHaveBeenCalledTimes(1);

    setDirty(false);
    expect(heading()).toBe("Person");
  });

  it("opens another screen at once even while the form is dirty", () => {
    const update = renderPage(formView("Contact"));
    setDirty(true);
    update(formView("Other contact", "c2"));
    expect(heading()).toBe("Other contact");
  });

  it("does not hold or announce a rebuilt but unchanged declaration", () => {
    const info = vi.spyOn(toast, "info");
    const update = renderPage(formView("Contact"));
    setDirty(true);
    update(formView("Contact"));
    expect(heading()).toBe("Contact");
    expect(info).not.toHaveBeenCalled();
  });
});
