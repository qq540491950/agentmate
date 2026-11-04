/**
 * shouldOpenLatestSession decides whether ChatPage may auto-select the newest
 * session after the list loads or changes. Clicking「新聊天」now opens an
 * unsaved draft (activeID empty + draft flag set): auto-selecting an old
 * session would replace the blank composer, and eagerly calling NewSession
 * would leave an empty row in the sidebar (issue #30).
 */
export function shouldOpenLatestSession(opts: {
  activeID: string;
  draft: boolean;
  sessionCount: number;
}): boolean {
  return opts.activeID === "" && !opts.draft && opts.sessionCount > 0;
}