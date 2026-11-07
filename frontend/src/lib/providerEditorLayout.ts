// Provider editor dialog layout (#32). The model list has no natural bound —
// nine fetched models are enough to push the footer outside a default window —
// so the dialog gets a viewport cap and only the form body scrolls.
//
// Both halves must stay together: a capped dialog without a scrollable body
// clips the footer, and a scrollable body without `min-h-0` cannot shrink
// inside the flex column (min-height defaults to content), so the dialog
// still grows past the viewport. Keep `overflow-y-auto` off the dialog itself
// so 「保存」/「取消」 never scroll out of reach.
export const PROVIDER_EDITOR_DIALOG_CLASS =
  "flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl";

export const PROVIDER_EDITOR_BODY_CLASS =
  "grid min-h-0 flex-1 gap-6 overflow-y-auto px-6 py-5";