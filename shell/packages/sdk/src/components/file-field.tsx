import { File as FileIcon, FileSpreadsheet, FileText, Image as ImageIcon } from "lucide-react";
import type { ChangeEvent, DragEvent, ReactNode } from "react";
import { useEffect, useId, useRef, useState } from "react";
import { actionButtonClassName } from "./action-button-styles.js";
import { AvatarCrop } from "./avatar-crop.js";
import type { UploadHandle } from "./file-field-upload.js";
import { formatFileSize, UploadError, uploadFile, validateFile } from "./file-field-upload.js";
import { ProgressBar } from "./progress-bar.js";

export interface FileValue {
  fileId: string;
  name: string;
  contentType: string;
  sizeBytes: number;
  url?: string | undefined;
}

type UploadFn = (
  file: File | Blob,
  filename: string,
  purpose: string,
  onProgress: (percent: number) => void,
) => UploadHandle;

export interface FileFieldProps {
  id?: string | undefined;
  value: FileValue | FileValue[] | null;
  onChange: (value: FileValue | FileValue[] | null) => void;
  variant?: "file" | "image" | "avatar" | undefined;
  multiple?: boolean | undefined;
  accept?: string | undefined;
  maxFileSizeMb?: number | undefined;
  disabled?: boolean | undefined;
  // Storybook has no real /storage/upload backend (file-field.md's own
  // Acceptance Criteria requires mocking the upload request and its
  // progress events there) — an injectable default, same shape as
  // RelationPicker's client/registry params.
  uploadFn?: UploadFn | undefined;
}

interface InFlightItem {
  key: string;
  file: File | Blob;
  filename: string;
  status: "uploading" | "failed";
  percent: number;
  error?: string | undefined;
}

const DEFAULT_MAX_FILE_SIZE_MB = 10;

function purposeFor(variant: "file" | "image" | "avatar"): string {
  return variant === "avatar" ? "avatars" : "attachments";
}

function FileTypeIcon({ contentType }: { contentType: string }): ReactNode {
  const Icon = contentType.startsWith("image/")
    ? ImageIcon
    : contentType.includes("spreadsheet") || contentType.includes("csv")
      ? FileSpreadsheet
      : contentType === "application/pdf" || contentType.startsWith("text/")
        ? FileText
        : FileIcon;
  return <Icon size={20} className="shrink-0 text-text-secondary" aria-hidden />;
}

export function FileField({
  id,
  value,
  onChange,
  variant = "file",
  multiple = false,
  accept,
  maxFileSizeMb = DEFAULT_MAX_FILE_SIZE_MB,
  disabled = false,
  uploadFn = uploadFile,
}: FileFieldProps): ReactNode {
  const inputId = useId();
  const resolvedId = id ?? inputId;
  const inputRef = useRef<HTMLInputElement>(null);
  const handlesRef = useRef(new Map<string, { abort: () => void }>());
  const previewUrlsRef = useRef(new Map<string, string>());
  // Concurrent uploads (multiple files dropped in one batch) each close
  // over `value` from the render that started them — two completions
  // racing would otherwise each append to a stale snapshot and the loser
  // silently overwrites the winner's result. Reading the latest value via
  // a ref, updated every render, avoids that.
  const valueRef = useRef(value);
  valueRef.current = value;
  const [inFlight, setInFlight] = useState<InFlightItem[]>([]);
  const [pendingAvatarFile, setPendingAvatarFile] = useState<File | null>(null);
  const [rejectionError, setRejectionError] = useState<string | undefined>();
  const [dragOver, setDragOver] = useState(false);

  useEffect(() => {
    const handles = handlesRef.current;
    const previewUrls = previewUrlsRef.current;
    return () => {
      for (const handle of handles.values()) handle.abort();
      for (const url of previewUrls.values()) URL.revokeObjectURL(url);
    };
  }, []);

  function startUpload(item: InFlightItem): void {
    const handle = uploadFn(item.file, item.filename, purposeFor(variant), (percent) => {
      setInFlight((items) => items.map((it) => (it.key === item.key ? { ...it, percent } : it)));
    });
    handlesRef.current.set(item.key, handle);
    handle.promise.then(
      (result) => {
        handlesRef.current.delete(item.key);
        if (variant !== "file") previewUrlsRef.current.set(result.fileId, URL.createObjectURL(item.file));
        setInFlight((items) => items.filter((it) => it.key !== item.key));
        const next: FileValue = {
          fileId: result.fileId,
          name: result.name,
          contentType: result.contentType,
          sizeBytes: result.sizeBytes,
        };
        onChange(multiple ? [...(Array.isArray(valueRef.current) ? valueRef.current : []), next] : next);
      },
      (err: unknown) => {
        handlesRef.current.delete(item.key);
        const message = err instanceof UploadError ? err.message : "Upload failed.";
        setInFlight((items) =>
          items.map((it) => (it.key === item.key ? { ...it, status: "failed", error: message } : it)),
        );
      },
    );
  }

  function handleFilesSelected(files: File[]): void {
    setRejectionError(undefined);
    if (disabled || files.length === 0) return;

    if (!multiple) {
      const file = files[0];
      if (!file) return;
      const error = validateFile(file, accept, maxFileSizeMb);
      if (error) {
        setRejectionError(error);
        return;
      }
      if (variant === "avatar") {
        setPendingAvatarFile(file);
        return;
      }
      const item: InFlightItem = {
        key: crypto.randomUUID(),
        file,
        filename: file.name,
        status: "uploading",
        percent: 0,
      };
      setInFlight([item]);
      startUpload(item);
      return;
    }

    // Collected and reported together — setting rejectionError once per
    // rejected file inside the loop would just have the last one win,
    // silently dropping every earlier file's specific reason.
    const errors: string[] = [];
    for (const file of files) {
      const error = validateFile(file, accept, maxFileSizeMb);
      if (error) {
        errors.push(`${file.name}: ${error}`);
        continue;
      }
      const item: InFlightItem = {
        key: crypto.randomUUID(),
        file,
        filename: file.name,
        status: "uploading",
        percent: 0,
      };
      setInFlight((items) => [...items, item]);
      startUpload(item);
    }
    if (errors.length > 0) setRejectionError(errors.join(" "));
  }

  function handleAvatarApply(blob: Blob): void {
    if (!pendingAvatarFile) return;
    const filename = `${pendingAvatarFile.name.replace(/\.[^.]+$/, "")}.png`;
    const item: InFlightItem = { key: crypto.randomUUID(), file: blob, filename, status: "uploading", percent: 0 };
    setPendingAvatarFile(null);
    setInFlight([item]);
    startUpload(item);
  }

  function retry(item: InFlightItem): void {
    setInFlight((items) =>
      items.map((it) => (it.key === item.key ? { ...it, status: "uploading", percent: 0, error: undefined } : it)),
    );
    startUpload(item);
  }

  function removeInFlight(key: string): void {
    handlesRef.current.get(key)?.abort();
    handlesRef.current.delete(key);
    setInFlight((items) => items.filter((it) => it.key !== key));
  }

  function removeCompleted(fileId: string): void {
    const previewUrl = previewUrlsRef.current.get(fileId);
    if (previewUrl) {
      URL.revokeObjectURL(previewUrl);
      previewUrlsRef.current.delete(fileId);
    }
    if (multiple) {
      onChange((Array.isArray(value) ? value : []).filter((v) => v.fileId !== fileId));
    } else {
      onChange(null);
    }
  }

  function handleInputChange(event: ChangeEvent<HTMLInputElement>): void {
    handleFilesSelected(event.target.files ? [...event.target.files] : []);
    event.target.value = "";
  }

  function handleDrop(event: DragEvent<HTMLDivElement>): void {
    event.preventDefault();
    setDragOver(false);
    if (disabled) return;
    handleFilesSelected([...event.dataTransfer.files]);
  }

  function handleDragOver(event: DragEvent<HTMLDivElement>): void {
    event.preventDefault();
    if (!disabled) setDragOver(true);
  }

  function handleDragLeave(): void {
    setDragOver(false);
  }

  function browse(): void {
    if (!disabled) inputRef.current?.click();
  }

  const completedValues: FileValue[] = multiple
    ? Array.isArray(value)
      ? value
      : []
    : value
      ? [value as FileValue]
      : [];
  const showCaptureArea = multiple || (completedValues.length === 0 && inFlight.length === 0 && !pendingAvatarFile);

  return (
    <div className="flex flex-col gap-2">
      <input
        ref={inputRef}
        id={resolvedId}
        type="file"
        accept={accept}
        multiple={multiple}
        // Stays reachable (sr-only, not display:none) even once the visible
        // "Browse files" button unmounts, so it must be disabled in step
        // with the capture area itself — otherwise a keyboard user can tab
        // to it directly and select a second file while a non-multiple
        // upload is already in flight, silently racing the first.
        disabled={disabled || (!multiple && !showCaptureArea)}
        onChange={handleInputChange}
        className="sr-only"
      />

      {pendingAvatarFile && (
        <AvatarCrop
          file={pendingAvatarFile}
          onApply={handleAvatarApply}
          onCancel={() => setPendingAvatarFile(null)}
          disabled={disabled}
        />
      )}

      {showCaptureArea && !pendingAvatarFile && (
        // biome-ignore lint/a11y/noStaticElementInteractions: drag-and-drop target only — the keyboard-operable equivalent is the real "Browse files" button nested inside it.
        <div
          onDrop={handleDrop}
          onDragOver={handleDragOver}
          onDragLeave={handleDragLeave}
          className={`flex flex-col items-center gap-2 rounded-structural border border-dashed p-4 text-center transition-colors duration-(--duration-fast) ease-out ${
            dragOver ? "border-primary bg-primary-subtle" : "border-border"
          } ${disabled ? "opacity-50" : ""}`}
        >
          <span className="text-sm text-text-secondary">Drag and drop a file here, or</span>
          <button
            type="button"
            disabled={disabled}
            data-disabled={disabled ? "true" : undefined}
            onClick={browse}
            className={actionButtonClassName("secondary", "sm")}
          >
            Browse files
          </button>
        </div>
      )}

      {rejectionError && (
        <span role="alert" className="text-xs text-danger">
          {rejectionError}
        </span>
      )}

      {inFlight.map((item) => (
        <div key={item.key} className="flex flex-col gap-1 rounded-control border border-border bg-surface p-2">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-sm text-text" title={item.filename}>
              {item.filename}
            </span>
            <button
              type="button"
              disabled={disabled}
              onClick={() => removeInFlight(item.key)}
              aria-label={`Remove ${item.filename}`}
              className="shrink-0 rounded-control p-1 text-text-secondary hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
            >
              ×
            </button>
          </div>
          <ProgressBar
            value={item.percent}
            status={item.status === "failed" ? "danger" : "default"}
            label={item.filename}
          />
          {item.status === "failed" && (
            <>
              <span role="alert" className="text-xs text-danger">
                {item.error}
              </span>
              <button
                type="button"
                disabled={disabled}
                data-disabled={disabled ? "true" : undefined}
                onClick={() => retry(item)}
                className={`self-start ${actionButtonClassName("secondary", "sm")}`}
              >
                Try again
              </button>
            </>
          )}
        </div>
      ))}

      {completedValues.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {completedValues.map((fileValue) => {
            const previewUrl = fileValue.url ?? previewUrlsRef.current.get(fileValue.fileId);
            if (variant === "avatar") {
              return (
                <span key={fileValue.fileId} className="relative inline-flex" style={{ width: 96, height: 96 }}>
                  {previewUrl && (
                    <img
                      src={previewUrl}
                      alt={fileValue.name}
                      style={{ width: 96, height: 96 }}
                      className="rounded-full object-cover"
                    />
                  )}
                  <button
                    type="button"
                    disabled={disabled}
                    onClick={() => removeCompleted(fileValue.fileId)}
                    aria-label={`Remove ${fileValue.name}`}
                    style={{
                      top: "var(--space-1)",
                      insetInlineEnd: "var(--space-1)",
                      position: "absolute",
                      width: "var(--space-6)",
                      height: "var(--space-6)",
                    }}
                    className="inline-flex items-center justify-center rounded-full bg-surface text-text-secondary shadow-sm hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    ×
                  </button>
                </span>
              );
            }
            if (variant === "image") {
              return (
                <span key={fileValue.fileId} className="relative inline-flex" style={{ width: 128, height: 128 }}>
                  {previewUrl && (
                    <img
                      src={previewUrl}
                      alt={fileValue.name}
                      style={{ width: 128, height: 128 }}
                      className="rounded-control object-cover"
                    />
                  )}
                  <button
                    type="button"
                    disabled={disabled}
                    onClick={() => removeCompleted(fileValue.fileId)}
                    aria-label={`Remove ${fileValue.name}`}
                    style={{
                      top: "var(--space-1)",
                      insetInlineEnd: "var(--space-1)",
                      position: "absolute",
                      width: "var(--space-6)",
                      height: "var(--space-6)",
                    }}
                    className="inline-flex items-center justify-center rounded-full bg-surface text-text-secondary shadow-sm hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    ×
                  </button>
                </span>
              );
            }
            return (
              <span
                key={fileValue.fileId}
                className="inline-flex items-center gap-2 rounded-control border border-border bg-surface px-2 py-1"
              >
                <FileTypeIcon contentType={fileValue.contentType} />
                <span className="flex flex-col">
                  <span className="truncate text-sm text-text" style={{ maxWidth: 200 }} title={fileValue.name}>
                    {fileValue.name}
                  </span>
                  <span className="text-xs text-text-secondary">{formatFileSize(fileValue.sizeBytes)}</span>
                </span>
                <button
                  type="button"
                  disabled={disabled}
                  onClick={() => removeCompleted(fileValue.fileId)}
                  aria-label={`Remove ${fileValue.name}`}
                  className="shrink-0 rounded-control p-1 text-text-secondary hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
                >
                  ×
                </button>
              </span>
            );
          })}
        </div>
      )}
    </div>
  );
}
