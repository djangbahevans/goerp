import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { withAdminShell } from "../users/admin-users-story-fixtures.js";
import { fakeModulesBackend, SALES } from "./admin-modules-story-fixtures.js";
import { type FakeModulesBackend, installFakeAdminModulesBackend } from "./fake-admin-modules-backend.js";
import { ModuleDetailPage } from "./module-detail-page.js";

const meta: Meta<typeof ModuleDetailPage> = {
  title: "Admin/Modules/Detail",
  component: ModuleDetailPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
  args: { name: "sales", onBackToList: () => {}, onOpenModule: () => {} },
};

export default meta;

type Story = StoryObj<typeof ModuleDetailPage>;

export const Active: Story = {
  name: "active module with a dependency",
  beforeEach: fakeModulesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    expect(await canvas.findByRole("button", { name: "Disable module" })).toBeInTheDocument();
    expect(canvas.getByRole("button", { name: "Contacts" })).toBeInTheDocument();
    expect(canvas.getByText("sales:order:read")).toBeInTheDocument();
  },
};

export const ConfirmDisable: Story = {
  name: "confirming a disable",
  beforeEach: fakeModulesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Disable module" }));
    const dialog = await within(document.body).findByRole("alertdialog");
    expect(
      within(dialog).getByText("This will hide all Sales data from users. Data is not deleted."),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Disable" }));
    await waitFor(() => expect(canvas.getByRole("button", { name: "Enable module" })).toBeInTheDocument());
  },
};

export const WithSettings: Story = {
  name: "module with settings",
  beforeEach: fakeModulesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    expect(await canvas.findByRole("heading", { name: "Numbering" })).toBeInTheDocument();
    expect(canvas.getByLabelText("Invoice prefix")).toHaveValue("ACME");
    expect(canvas.getByLabelText("Reporting API key")).toHaveValue("***");
    expect(canvas.getByRole("button", { name: "Save configuration" })).toBeDisabled();
  },
};

export const SavingSettings: Story = {
  name: "saving a setting",
  beforeEach: fakeModulesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const prefix = await canvas.findByLabelText("Invoice prefix");
    await userEvent.clear(prefix);
    await userEvent.type(prefix, "ZED");
    await userEvent.click(canvas.getByRole("button", { name: "Save configuration" }));
    await waitFor(() => expect(canvas.getByRole("button", { name: "Save configuration" })).toBeDisabled());
    expect(canvas.getByLabelText("Invoice prefix")).toHaveValue("ZED");
  },
};

export const Disabled: Story = {
  name: "disabled module",
  args: { name: "hr" },
  beforeEach: fakeModulesBackend(),
  play: async ({ canvasElement }) => {
    expect(await within(canvasElement).findByRole("button", { name: "Enable module" })).toBeInTheDocument();
  },
};

export const NotOnPlan: Story = {
  name: "module not on the plan",
  args: { name: "payroll_plus" },
  beforeEach: fakeModulesBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    expect(await canvas.findByText("Your plan doesn't include Payroll Plus.")).toBeInTheDocument();
    expect(canvas.queryByRole("button", { name: "Enable module" })).toBeNull();
  },
};

export const NotFound: Story = {
  name: "not installed",
  args: { name: "nope" },
  beforeEach: fakeModulesBackend(),
  play: async ({ canvasElement }) => {
    expect(await within(canvasElement).findByText("Module not found")).toBeInTheDocument();
  },
};

let countryBackend: FakeModulesBackend;

export const CountrySetting: Story = {
  name: "country setting saves its ISO code",
  beforeEach: () => {
    countryBackend = installFakeAdminModulesBackend({
      modules: [
        {
          ...SALES,
          config: [
            ...(SALES.config ?? []),
            {
              key: "default_country",
              label: "Default Country",
              type: "string",
              fieldType: "country_select",
              default: "GH",
            },
          ],
        },
      ],
    });
    return countryBackend.restore;
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const picker = await canvas.findByRole("combobox", { name: "Default Country" });
    await userEvent.click(picker);
    await userEvent.clear(picker);
    await userEvent.type(picker, "Kenya");
    await userEvent.click(await within(document.body).findByRole("option", { name: /Kenya/ }));
    await waitFor(() => expect(picker).toHaveFocus());
    await userEvent.click(canvas.getByRole("button", { name: "Save configuration" }));
    await waitFor(() =>
      expect(countryBackend.requests.find((request) => request.path === "/admin/config")).toMatchObject({
        method: "PATCH",
        body: { "sales.default_country": "KE" },
      }),
    );
    const saved = countryBackend.requests.find((request) => request.path === "/admin/config");
    expect(saved?.body).toEqual({ "sales.default_country": "KE" });
    await waitFor(() => expect(canvas.getByRole("button", { name: "Save configuration" })).toBeDisabled());
    expect(canvas.getByRole("combobox", { name: "Default Country" })).toHaveValue("Kenya");
  },
};
