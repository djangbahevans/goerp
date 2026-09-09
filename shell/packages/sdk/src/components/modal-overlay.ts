// Shared by every Radix dialog-family component (AlertDialog,
// BulkActionPanel, and CommandPalette in @goerp/shell-app) — a plain
// opacity fade over --z-modal, using the shared fade-in/fade-out
// keyframes tokens.css already defines for exactly this purpose.
export const MODAL_OVERLAY_CLASSES =
  "fixed inset-0 z-(--z-modal) bg-overlay data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in]";
