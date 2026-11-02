// 应用内“反馈问题”入口使用的 GitHub issue 表单预填链接。
// 字段 id 与 .github/ISSUE_TEMPLATE/bug_report.yml 保持一致；GitHub 不支持
// 预填 dropdown（复现频率），该字段由提交者在页面上选择。

const ISSUE_NEW_URL = "https://github.com/qq540491950/go-workbuddy/issues/new";

const OS_NAMES: Record<string, string> = {
  windows: "Windows",
  darwin: "macOS",
  linux: "Linux",
};

/**
 * Build the pre-filled "new issue" URL. `platform` is `${GOOS}/${GOARCH}`
 * from AppInfo; an empty version or platform simply omits that field.
 */
export function buildIssueReportURL(version: string, platform: string): string {
  const params = new URLSearchParams();
  params.set("template", "bug_report.yml");

  const v = version.trim();
  if (v) {
    params.set("field:version", v.startsWith("v") || v === "dev" ? v : `v${v}`);
  }

  const [goos = "", goarch = ""] = platform.split("/");
  if (goos) {
    const osName = OS_NAMES[goos] ?? goos;
    params.set(
      "field:environment",
      [
        `${osName}${goarch ? ` · ${goarch}` : ""}`,
        "（由应用内「反馈问题」入口自动填入，可修改）",
      ].join("\n"),
    );
  }

  return `${ISSUE_NEW_URL}?${params.toString()}`;
}