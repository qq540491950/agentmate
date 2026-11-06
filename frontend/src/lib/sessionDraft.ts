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

/**
 * draftUsesProject decides which RPC creates the session for a not-yet-saved
 * chat: a draft opened from a project's「+」binds to that project, while a
 * global「新聊天」creates a standalone task session. Neither persists a row
 * until the first message is actually sent (issue #60: every click used to
 * create an empty「新对话」).
 */
export function draftUsesProject(projectID: string): boolean {
  return projectID.trim() !== "";
}