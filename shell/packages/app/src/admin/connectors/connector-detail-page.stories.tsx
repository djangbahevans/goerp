import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { withAdminShell } from "../users/admin-users-story-fixtures.js";
import { fakeConnectorsBackend, PAYSTACK } from "./admin-connectors-story-fixtures.js";
import { ConnectorDetailPage } from "./connector-detail-page.js";
import { type FakeConnectorsBackend, installFakeAdminConnectorsBackend } from "./fake-admin-connectors-backend.js";

const meta: Meta<typeof ConnectorDetailPage> = {
  title: "Admin/Connectors/Detail",
  component: ConnectorDetailPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
  args: { name: "connector_paystack", onBackToList: () => {} },
};

export default meta;

type Story = StoryObj<typeof ConnectorDetailPage>;

export const Configured: Story = {
  name: "every widget, secret masked",
  beforeEach: fakeConnectorsBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const secret = (await canvas.findByLabelText("Secret Key")) as HTMLInputElement;
    expect(secret.type).toBe("password");
    expect(secret.value).toBe("***");
    expect(canvas.getByRole("switch")).not.toBeChecked();
    expect(canvas.getByRole("button", { name: "Rotate" })).toBeInTheDocument();
    expect(canvas.getByRole("button", { name: "Save configuration" })).toBeDisabled();
  },
};

export const WebhookUrl: Story = {
  name: "webhook URL once minted",
  beforeEach: fakeConnectorsBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const url = await canvas.findByLabelText("Webhook URL");
    expect(url).toHaveTextContent("/_webhooks/connector_paystack/tok43paystack");
    expect(canvas.getByRole("button", { name: "Copy" })).toBeInTheDocument();
    expect(canvas.getByRole("button", { name: "Revoke" })).toBeInTheDocument();
  },
};

export const UnsavedChange: Story = {
  name: "unsaved change",
  beforeEach: fakeConnectorsBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("switch"));
    expect(canvas.getByRole("button", { name: "Save configuration" })).toBeEnabled();
  },
};

export const TestConnection: Story = {
  name: "test connection result",
  beforeEach: fakeConnectorsBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Test connection" }));
    await waitFor(() => expect(canvas.getByRole("status", { name: "Connection test" })).toBeInTheDocument());
  },
};

export const SecondaryProvider: Story = {
  name: "provider that is not primary",
  args: { name: "connector_africastalking" },
  beforeEach: fakeConnectorsBackend(),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    expect(await canvas.findByRole("button", { name: "Set as primary provider" })).toBeInTheDocument();
    expect(canvas.getByText("Not configured")).toBeInTheDocument();
  },
};

export const NotFound: Story = {
  name: "connector not installed",
  args: { name: "connector_missing" },
  beforeEach: fakeConnectorsBackend(),
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(within(canvasElement).getByText("Connector not found")).toBeInTheDocument());
  },
};

let countryBackend: FakeConnectorsBackend;

export const CountrySetting: Story = {
  name: "country setting saves its ISO code",
  beforeEach: () => {
    countryBackend = installFakeAdminConnectorsBackend({
      connectors: [
        {
          ...PAYSTACK,
          config: [
            ...(PAYSTACK.config ?? []),
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
        body: { "connector_paystack.default_country": "KE" },
      }),
    );
    const saved = countryBackend.requests.find((request) => request.path === "/admin/config");
    expect(saved?.body).toEqual({ "connector_paystack.default_country": "KE" });
    await waitFor(() => expect(canvas.getByRole("button", { name: "Save configuration" })).toBeDisabled());
    expect(canvas.getByRole("combobox", { name: "Default Country" })).toHaveValue("Kenya");
  },
};
