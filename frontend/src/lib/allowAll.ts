// Client-side rules of the session-level「允许完全访问」switch (issue #48).
// Pure so the wording and the one-time risk gate stay testable without a DOM.

/** Where the switch was flipped; the backend records it in the audit trail. */
export type AllowAllSource = "confirm_card" | "composer";

/** localStorage key of the one-time risk acknowledgement. */
export const ALLOW_ALL_RISK_KEY = "agentmate.allowAll.riskAcknowledged";

export const ALLOW_ALL_RISK_TITLE = "允许完全访问本会话";

export const ALLOW_ALL_RISK_DESCRIPTION =
  "开启后，本会话内的写文件与执行命令不再逐次确认。请注意数据安全，建议仅在可信的任务中开启；"
  + "该权限只作用于当前会话，可随时在输入框或确认卡处关闭。";

/**
 * allowAllRiskAlreadyAcknowledged reports whether the risk warning was
 * accepted before. Storage can throw (private mode, disabled), and a throwing
 * reader must not silently grant access — it means "not acknowledged yet".
 */
export function allowAllRiskAlreadyAcknowledged(get: (key: string) => string | null): boolean {
  try {
    return get(ALLOW_ALL_RISK_KEY) === "1";
  } catch {
    return false;
  }
}

/** rememberAllowAllRiskAcknowledged stops the dialog from coming back. */
export function rememberAllowAllRiskAcknowledged(set: (key: string, value: string) => void): void {
  try {
    set(ALLOW_ALL_RISK_KEY, "1");
  } catch {
    // A device that cannot persist the acknowledgement just asks again —
    // the safer direction for a permission prompt.
  }
}

/** allowAllToggleLabel is the shared wording of the card button and the composer chip. */
export function allowAllToggleLabel(granted: boolean): string {
  return granted ? "完全访问已开启" : "允许完全访问";
}

/** allowAllToggleHint explains the current level and how to leave it. */
export function allowAllToggleHint(granted: boolean): string {
  return granted
    ? "本会话的写文件与执行命令已不再逐次确认；点击关闭并恢复逐次确认"
    : "开启后本会话不再逐次确认写文件与执行命令；仅影响当前会话，可随时关闭";
}
