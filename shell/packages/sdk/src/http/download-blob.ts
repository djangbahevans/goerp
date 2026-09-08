// Triggers a browser file-save for a Blob already fetched via
// apiClient.getBlob — export/report actions (view-system.md's "Export
// actions"/"Report actions") both need this, not just bulk exports.
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}
