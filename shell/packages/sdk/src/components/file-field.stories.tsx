import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { FileField } from "./file-field.js";
import type { UploadHandle, UploadResult } from "./file-field-upload.js";
import { UploadError } from "./file-field-upload.js";

// Storybook has no real /storage/upload backend (file-field.md's own
// Acceptance Criteria) — a fake uploadFn resolving/rejecting after a
// visible progress tick, injected the same way RelationPicker's stories
// inject a fake client/registry.
function fakeUpload(outcome: "success" | "fail-413" | "fail-415" | "hang") {
  return (
    _file: File | Blob,
    filename: string,
    _purpose: string,
    onProgress: (percent: number) => void,
  ): UploadHandle => {
    onProgress(35);
    if (outcome === "hang") {
      return { promise: new Promise<UploadResult>(() => {}), abort: () => {} };
    }
    const promise = new Promise<UploadResult>((resolve, reject) => {
      setTimeout(() => {
        if (outcome === "success") {
          onProgress(100);
          resolve({ fileId: crypto.randomUUID(), name: filename, contentType: "application/pdf", sizeBytes: 245_760 });
        } else {
          reject(new UploadError(outcome === "fail-413" ? 413 : 415));
        }
      }, 300);
    });
    return { promise, abort: () => {} };
  };
}

function fakeFile(name = "Contract.pdf", type = "application/pdf", sizeBytes = 245_760): File {
  return new File([new Uint8Array(sizeBytes)], name, { type });
}

async function selectFile(canvasElement: HTMLElement, file: File) {
  const input = canvasElement.querySelector('input[type="file"]') as HTMLInputElement;
  await userEvent.upload(input, file);
}

const meta: Meta<typeof FileField> = {
  title: "Form Fields/FileField",
  component: FileField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof FileField>;

export const Empty: Story = {
  args: {
    value: null,
    uploadFn: fakeUpload("hang"),
  },
};

export const DragOver: Story = {
  args: {
    value: null,
    uploadFn: fakeUpload("hang"),
  },
  play: async ({ canvasElement }) => {
    const dropzone = canvasElement.querySelector("[class*='border-dashed']") as HTMLElement;
    dropzone.dispatchEvent(
      new DragEvent("dragover", { bubbles: true, cancelable: true, dataTransfer: new DataTransfer() }),
    );
  },
};

export const ClientSideRejected: Story = {
  args: {
    value: null,
    maxFileSizeMb: 1,
    uploadFn: fakeUpload("hang"),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await selectFile(canvasElement, fakeFile("big.pdf", "application/pdf", 2 * 1024 * 1024));
    await waitFor(() => expect(canvas.getByRole("alert").textContent).toContain("exceeds"));
  },
};

export const Uploading: Story = {
  args: {
    value: null,
    uploadFn: fakeUpload("hang"),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await selectFile(canvasElement, fakeFile());
    await waitFor(() => expect(canvas.getByRole("progressbar")).toBeInTheDocument());
  },
};

export const Failed: Story = {
  args: {
    value: null,
    uploadFn: fakeUpload("fail-415"),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await selectFile(canvasElement, fakeFile());
    await waitFor(() => expect(canvas.getByRole("button", { name: "Try again" })).toBeInTheDocument());
  },
};

export const Complete: Story = {
  args: {
    value: { fileId: "01j-example", name: "Contract.pdf", contentType: "application/pdf", sizeBytes: 245_760 },
  },
};

// A real image loaded from the server always has a signed `url` (the
// read shape object-storage-guide.md §3 documents) — a data URI stands
// in for that here, since Storybook has no real backend to sign one.
const PLACEHOLDER_IMAGE_URL =
  "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='200' height='200'%3E%3Crect width='200' height='200' fill='%234a90d9'/%3E%3Ctext x='50%25' y='50%25' font-size='24' fill='white' text-anchor='middle' dy='.3em'%3EIMG%3C/text%3E%3C/svg%3E";

export const ImageComplete: Story = {
  args: {
    variant: "image",
    value: {
      fileId: "01j-example",
      name: "product.jpg",
      contentType: "image/jpeg",
      sizeBytes: 1_048_576,
      url: PLACEHOLDER_IMAGE_URL,
    },
  },
};

export const AvatarCropStep: Story = {
  args: {
    variant: "avatar",
    value: null,
    uploadFn: fakeUpload("hang"),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await selectFile(canvasElement, fakeFile("me.png", "image/png", 4096));
    await waitFor(() => expect(canvas.getByRole("button", { name: "Apply" })).toBeInTheDocument());
  },
};

export const AvatarComplete: Story = {
  args: {
    variant: "avatar",
    value: {
      fileId: "01j-example",
      name: "me.png",
      contentType: "image/png",
      sizeBytes: 40_960,
      url: PLACEHOLDER_IMAGE_URL,
    },
  },
};

export const MultipleWithChips: Story = {
  args: {
    multiple: true,
    value: [
      { fileId: "01j-1", name: "Quote.pdf", contentType: "application/pdf", sizeBytes: 128_000 },
      { fileId: "01j-2", name: "Specification.docx", contentType: "application/vnd.openxmlformats", sizeBytes: 54_000 },
    ],
  },
};

export const Disabled: Story = {
  args: {
    value: { fileId: "01j-example", name: "Contract.pdf", contentType: "application/pdf", sizeBytes: 245_760 },
    disabled: true,
  },
};
