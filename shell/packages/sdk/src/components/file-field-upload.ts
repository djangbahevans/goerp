// object-storage-guide.md §4's upload flow. apiClient is fetch-based
// (api-client.ts), and fetch has no upload-progress event — only
// XMLHttpRequest's xhr.upload.onprogress does — so this bypasses apiClient
// for the upload call specifically.

export interface UploadResult {
  fileId: string;
  name: string;
  contentType: string;
  sizeBytes: number;
}

export class UploadError extends Error {
  // 0 for a network error (no response at all).
  status: number;

  constructor(status: number) {
    super(
      status === 413
        ? "File is too large."
        : status === 415
          ? "File type not allowed."
          : "Upload failed. Check your connection and try again.",
    );
    this.status = status;
  }
}

export interface UploadHandle {
  promise: Promise<UploadResult>;
  abort: () => void;
}

// The browser shell shares its origin with the engine (api-client.ts's own
// paths are all relative, with no configured base URL), and the session
// cookie authenticates automatically for a same-origin XHR via
// withCredentials — the same "browser" clientType credentials: "include"
// apiClient itself uses.
export function uploadFile(
  file: File | Blob,
  filename: string,
  purpose: string,
  onProgress: (percent: number) => void,
): UploadHandle {
  const xhr = new XMLHttpRequest();
  const promise = new Promise<UploadResult>((resolve, reject) => {
    xhr.open("POST", "/storage/upload");
    xhr.withCredentials = true;
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress(Math.round((event.loaded / event.total) * 100));
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          const body = JSON.parse(xhr.responseText) as {
            file_id: string;
            name: string;
            content_type: string;
            size_bytes: number;
          };
          resolve({
            fileId: body.file_id,
            name: body.name,
            contentType: body.content_type,
            sizeBytes: body.size_bytes,
          });
        } catch {
          reject(new UploadError(xhr.status));
        }
      } else {
        reject(new UploadError(xhr.status));
      }
    };
    xhr.onerror = () => reject(new UploadError(0));
    xhr.onabort = () => reject(new UploadError(0));
    const formData = new FormData();
    formData.append("file", file, filename);
    formData.append("purpose", purpose);
    xhr.send(formData);
  });
  return { promise, abort: () => xhr.abort() };
}

// FormField.accept (manifest-spec.md §10): a comma-separated list of
// extensions (".pdf") and/or MIME types/wildcards ("image/*",
// "application/pdf"), the same shape the native <input accept> attribute
// takes — mirrored here since a file dropped (not picked via the native
// input) never gets the browser's own accept filtering for free.
export function matchesAccept(file: File, accept: string): boolean {
  const patterns = accept
    .split(",")
    .map((p) => p.trim())
    .filter((p) => p !== "");
  if (patterns.length === 0) return true;
  const name = file.name.toLowerCase();
  const type = file.type.toLowerCase();
  return patterns.some((pattern) => {
    const p = pattern.toLowerCase();
    if (p.startsWith(".")) return name.endsWith(p);
    if (p.endsWith("/*")) return type.startsWith(p.slice(0, -1));
    return type === p;
  });
}

export function validateFile(file: File, accept: string | undefined, maxFileSizeMb: number): string | undefined {
  if (file.size > maxFileSizeMb * 1024 * 1024) return `File exceeds the ${maxFileSizeMb} MB limit.`;
  if (accept && !matchesAccept(file, accept)) return "File type not accepted.";
  return undefined;
}

export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
