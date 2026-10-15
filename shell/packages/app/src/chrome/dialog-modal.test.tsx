import { type APIResponse, apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { executeInstructions, modalManager, type UIInstruction, useExecutionContext } from "@goerp/sdk/realtime";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, type MockInstance, vi } from "vitest";
import { ModalRoot } from "./modal-root.js";

vi.mock("@goerp/sdk/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@goerp/sdk/auth")>()),
  useAuth: () => ({ tenant: { defaultCurrency: "GHS" } }),
}));

const momo: UIInstruction = {
  type: "open_dialog",
  title: "Request MoMo Payment",
  submit_label: "Send Request",
  action: { method: "POST", path: "/sales/invoices/01j/request-payment" },
  fields: [
    { name: "phone", label: "MoMo Number", type: "phone", required: true, default: "{record.customer.phone}" },
    { name: "amount", label: "Amount", type: "monetary", required: true, default: "{record.amount_due}" },
  ],
};

function InvoiceForm() {
  useExecutionContext({
    view: "form",
    resource: "sales.invoice",
    recordId: "01j",
    record: { id: "01j", amount_due: 50000, customer: { phone: "0244123456" } },
    queryKey: ["record", "sales.invoice", "01j"],
  });
  return null;
}

function envelope(ui: UIInstruction[]): APIResponse<unknown> {
  return { body: { ui }, headers: new Headers({ "X-GoERP-UI": "1" }) };
}

let request: MockInstance<typeof apiClient.request>;

beforeEach(() => {
  request = vi.spyOn(apiClient, "request");
  render(
    <>
      <InvoiceForm />
      <ModalRoot />
    </>,
  );
});

afterEach(() => {
  cleanup();
  while (modalManager.getStack().length > 0) modalManager.closeTop();
  toast.dismiss();
  vi.restoreAllMocks();
});

function run(instructions: UIInstruction[]): Promise<void> {
  let done!: Promise<void>;
  act(() => {
    done = executeInstructions(instructions, "response");
  });
  return done;
}

it("renders the MoMo dialog pre-filled from the record and submits the documented body", async () => {
  request.mockResolvedValue(
    envelope([{ type: "close_modal" }, { type: "toast", message: "Payment request sent", severity: "success" }]),
  );
  const success = vi.spyOn(toast, "success");
  const done = run([momo]);

  await screen.findByRole("dialog", { name: "Request MoMo Payment" });
  expect(screen.getByLabelText(/^MoMo Number/)).toHaveProperty("value", "0244123456");
  expect(screen.getByLabelText(/^Amount/)).toHaveProperty("value", "500");
  expect(screen.getByText("GHS")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Send Request" }));
  await done;

  expect(request).toHaveBeenCalledWith("POST", "/sales/invoices/01j/request-payment", {
    phone: "0244123456",
    amount: 50000,
  });
  expect(success).toHaveBeenCalledWith("Payment request sent", { duration: 4000 });
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

it("lets values override the interpolated defaults", async () => {
  void run([{ ...momo, values: { phone: "0200000000" } } as UIInstruction]);
  await screen.findByRole("dialog");
  expect(screen.getByLabelText(/^MoMo Number/)).toHaveProperty("value", "0200000000");
});

it("keeps submit disabled until every required field is filled", async () => {
  void run([
    {
      ...momo,
      fields: [
        { name: "reference", label: "Reference", type: "text", required: true, default: "{record.reference}" },
        { name: "notes", label: "Notes", type: "textarea" },
      ],
    } as UIInstruction,
  ]);
  const submit = await screen.findByRole("button", { name: "Send Request" });
  expect(screen.getByLabelText(/Reference/)).toHaveProperty("value", "");
  expect(submit).toHaveProperty("disabled", true);

  fireEvent.change(screen.getByLabelText(/Reference/), { target: { value: "INV-1" } });
  expect(submit).toHaveProperty("disabled", false);
});

it("skips the instructions after open_dialog when the dialog is cancelled", async () => {
  const success = vi.spyOn(toast, "success");
  const done = run([momo, { type: "toast", message: "Requested", severity: "success" }]);

  fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
  await done;

  expect(success).not.toHaveBeenCalled();
  expect(request).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

it("shows a 4xx inside the dialog, keeps it open, and lets the user resubmit", async () => {
  request
    .mockRejectedValueOnce(
      new AppError({
        code: "payment.invalid_phone",
        message: "That number is not registered for Mobile Money",
        httpStatus: 400,
      }),
    )
    .mockResolvedValueOnce(envelope([{ type: "close_modal" }]));
  const success = vi.spyOn(toast, "success");
  const done = run([momo, { type: "toast", message: "Requested", severity: "success" }]);

  fireEvent.click(await screen.findByRole("button", { name: "Send Request" }));
  expect((await screen.findByRole("alert")).textContent).toBe("That number is not registered for Mobile Money");
  expect(screen.getByRole("dialog")).toBeTruthy();
  expect(success).not.toHaveBeenCalled();

  fireEvent.change(screen.getByLabelText(/^MoMo Number/), { target: { value: "0551234567" } });
  fireEvent.click(screen.getByRole("button", { name: "Send Request" }));
  await done;

  expect(request).toHaveBeenLastCalledWith("POST", "/sales/invoices/01j/request-payment", {
    phone: "0551234567",
    amount: 50000,
  });
  expect(success).toHaveBeenCalledOnce();
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

it("shows 422 field errors beside their fields", async () => {
  request.mockRejectedValueOnce(
    new AppError({
      code: "validation_failed",
      message: "Check the highlighted fields",
      httpStatus: 422,
      fieldErrors: { amount: ["must be at least 1.00"] },
    }),
  );
  void run([momo]);

  fireEvent.click(await screen.findByRole("button", { name: "Send Request" }));

  expect(await screen.findByText("must be at least 1.00")).toBeTruthy();
  expect(screen.getByLabelText(/^Amount/).getAttribute("aria-invalid")).toBe("true");
});

it("closes on a 2xx close_modal with an error toast, continuing the sequence", async () => {
  request.mockResolvedValue(
    envelope([{ type: "close_modal" }, { type: "toast", message: "Provider unavailable", severity: "error" }]),
  );
  const error = vi.spyOn(toast, "error");
  const info = vi.spyOn(toast, "info");
  const done = run([momo, { type: "toast", message: "After", severity: "info" }]);

  fireEvent.click(await screen.findByRole("button", { name: "Send Request" }));
  await done;

  expect(error).toHaveBeenCalledWith("Provider unavailable", { duration: 4000 });
  expect(info).toHaveBeenCalledOnce();
  expect(error.mock.invocationCallOrder[0]).toBeLessThan(info.mock.invocationCallOrder[0] ?? 0);
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

it("opens a dialog returned by the submit over the first", async () => {
  request.mockResolvedValueOnce(
    envelope([{ type: "close_modal" }, { ...momo, title: "Confirm OTP", fields: [] } as UIInstruction]),
  );
  void run([momo]);

  fireEvent.click(await screen.findByRole("button", { name: "Send Request" }));

  expect(await screen.findByRole("dialog", { name: "Confirm OTP" })).toBeTruthy();
  expect(screen.queryByRole("dialog", { name: "Request MoMo Payment" })).toBeNull();
});
