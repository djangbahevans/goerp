import type { DialogConfig, DialogField } from "@goerp/sdk/realtime";
import { describe, expect, it } from "vitest";
import { dialogRequestBody, initialDialogValues, isDialogFieldFilled } from "./dialog-values.js";

function config(fields: DialogField[], overrides: Partial<DialogConfig> = {}): DialogConfig {
  return {
    title: "Record Payment",
    submitLabel: "Record",
    action: { method: "POST", path: "/sales/invoices/01j/payments" },
    fields,
    record: { amount_due: 50000, customer: { phone: "0244123456" }, paid: true },
    values: {},
    ...overrides,
  };
}

describe("initialDialogValues", () => {
  it("interpolates each default from the record, as minor units for a monetary field", () => {
    expect(
      initialDialogValues(
        config([
          { name: "phone", label: "Phone", type: "phone", default: "{record.customer.phone}" },
          { name: "amount", label: "Amount", type: "monetary", default: "{record.amount_due}" },
          { name: "paid", label: "Paid", type: "boolean", default: "{record.paid}" },
          { name: "notes", label: "Notes", type: "text" },
        ]),
      ),
    ).toEqual({ phone: "0244123456", amount: 50000, paid: true, notes: "" });
  });

  it("lets values override an interpolated default", () => {
    expect(
      initialDialogValues(
        config(
          [
            { name: "phone", label: "Phone", type: "phone", default: "{record.customer.phone}" },
            { name: "amount", label: "Amount", type: "monetary", default: "{record.amount_due}" },
          ],
          { values: { phone: "0200000000", amount: 1250 } },
        ),
      ),
    ).toEqual({ phone: "0200000000", amount: 1250 });
  });

  it("defaults a missing record field, or a missing record, to empty", () => {
    const fields: DialogField[] = [
      { name: "ref", label: "Reference", type: "text", default: "{record.reference}" },
      { name: "amount", label: "Amount", type: "monetary", default: "{record.total}" },
    ];
    expect(initialDialogValues(config(fields))).toEqual({ ref: "", amount: undefined });
    expect(initialDialogValues(config(fields, { record: null }))).toEqual({ ref: "", amount: undefined });
  });

  it("fills {today} as a YYYY-MM-DD date", () => {
    const { date } = initialDialogValues(config([{ name: "date", label: "Date", type: "date", default: "{today}" }]));
    expect(date).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });
});

describe("isDialogFieldFilled", () => {
  it("treats blank text, an unset amount and an unchecked box as unfilled", () => {
    expect(isDialogFieldFilled({ name: "a", label: "A", type: "text" }, "  ")).toBe(false);
    expect(isDialogFieldFilled({ name: "a", label: "A", type: "monetary" }, undefined)).toBe(false);
    expect(isDialogFieldFilled({ name: "a", label: "A", type: "monetary" }, 0)).toBe(true);
    expect(isDialogFieldFilled({ name: "a", label: "A", type: "boolean" }, false)).toBe(false);
    expect(isDialogFieldFilled({ name: "a", label: "A", type: "number" }, "1,000")).toBe(false);
    expect(isDialogFieldFilled({ name: "a", label: "A", type: "number" }, "1000")).toBe(true);
  });
});

describe("dialogRequestBody", () => {
  it("sends numbers as numbers, booleans as booleans and empty fields as null", () => {
    const fields: DialogField[] = [
      { name: "qty", label: "Qty", type: "number" },
      { name: "amount", label: "Amount", type: "monetary" },
      { name: "agree", label: "Agree", type: "boolean" },
      { name: "date", label: "Date", type: "date" },
      { name: "method", label: "Method", type: "select" },
    ];
    expect(dialogRequestBody(fields, { qty: "2.5", amount: 50000, agree: false, date: "", method: "cash" })).toEqual({
      qty: 2.5,
      amount: 50000,
      agree: false,
      date: null,
      method: "cash",
    });
  });
});
