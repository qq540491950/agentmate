// Visibility class of the action cluster at the right edge of a session row
// (archive / pin / "…" menu). The cluster stays out of the layout until the
// row is hovered or holds focus, which keeps four controls per row instead of
// a permanently reserved strip.
//
// The "…" button inside this cluster is the anchor of a Radix dropdown, so the
// cluster must regain its layout box while that menu is open: Radix's modal
// layer sets `body { pointer-events: none }` (the row stops matching :hover)
// and moves focus into the portalled menu content (the row stops matching
// :focus-within). With only the hover/focus branches the span stays
// display:none, the popper measures a 0×0 anchor and the menu is painted in
// the window's top-left corner (issue #28). The group-has variant keeps the
// span display:flex for as long as the trigger reports data-state="open".
export const SESSION_ROW_ACTIONS_CLASS =
  "hidden shrink-0 items-center gap-0.5 group-hover:flex group-focus-within:flex group-has-data-[state=open]:flex";