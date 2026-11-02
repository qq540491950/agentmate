import { describe, expect, it } from "vitest";
import { buildIssueReportURL } from "./issue";

const params = (url: string) => new URL(url).searchParams;

describe("buildIssueReportURL", () => {
  it("prefills the bug report form with version and platform", () => {
    const p = params(buildIssueReportURL("0.5.0", "windows/amd64"));
    expect(p.get("template")).toBe("bug_report.yml");
    expect(p.get("field:version")).toBe("v0.5.0");
    expect(p.get("field:environment")).toContain("Windows · amd64");
  });

  it("keeps an existing v prefix and does not prefix dev builds", () => {
    expect(params(buildIssueReportURL("v0.5.0", "")).get("field:version")).toBe("v0.5.0");
    expect(params(buildIssueReportURL("dev", "")).get("field:version")).toBe("dev");
  });

  it("omits empty version and platform fields", () => {
    const p = params(buildIssueReportURL("  ", ""));
    expect(p.get("field:version")).toBeNull();
    expect(p.get("field:environment")).toBeNull();
  });

  it("maps known GOOS names and keeps unknown ones", () => {
    expect(params(buildIssueReportURL("", "darwin/arm64")).get("field:environment")).toContain("macOS · arm64");
    expect(params(buildIssueReportURL("", "plan9/386")).get("field:environment")).toContain("plan9 · 386");
  });
});