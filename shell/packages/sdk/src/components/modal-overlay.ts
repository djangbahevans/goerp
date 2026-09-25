// Shared by every Radix dialog-family component (AlertDialog,
// BulkActionPanel, and CommandPalette in @goerp/shell-app) — a plain
// opacity fade over --z-modal, using the shared fade-in/fade-out
// keyframes tokens.css already defines for exactly this purpose.
export const MODAL_OVERLAY_CLASSES =
  "fixed inset-0 z-(--z-modal) bg-overlay data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in]";

// Content is the full-viewport flex-centering/focus-trap boundary; the
// visible panel is a plain inner div, so the boundary can size to the whole
// viewport (required for centering) independently of the panel's own
// max-width/max-height. Fade+scale in and out, opacity-only under reduced
// motion. Shared by AlertDialog and KeyboardShortcutsDialog.
export const MODAL_CONTENT_CLASSES =
  "fixed inset-0 z-(--z-modal) flex items-center justify-center p-4 focus:outline-none data-[state=open]:animate-[alert-dialog-content-show_var(--duration-slow)_ease-out] data-[state=closed]:animate-[alert-dialog-content-hide_var(--duration-slow)_ease-in] motion-reduce:data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] motion-reduce:data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in]";
