import { describe, expect, it } from "vitest";
import { envToText, parseEnvText } from "./mcpEnv";

describe("parseEnvText", () => {
  it("keeps '=' inside values (base64 padding, connection strings)", () => {
    expect(parseEnvText("TOKEN=abc=def==\nURL=postgres://u:p@h/db")).toEqual({
      TOKEN: "abc=def==",
      URL: "postgres://u:p@h/db",
    });
  });
  it("drops lines without a key or a separator", () => {
    expect(parseEnvText("=nokey\nKEY=ok\nnoseparator\n  \nKEY2 = spaced")).toEqual({
      KEY: "ok",
      KEY2: " spaced",
    });
  });
});

describe("envToText round-trip", () => {
  it("round-trips newline-free values losslessly", () => {
    const env = { TOKEN: "abc=def==", URL: "postgres://u:p@h/db" };
    expect(parseEnvText(envToText(env))).toEqual(env);
  });
  it("documents the newline-in-value limitation", () => {
    // 包含换行符的值无法在基于行的编辑器中保留：尾部会变成孤立行并被丢弃。仅限
    // 导入的配置。
    const env = { A: "line1\nline2" };
    expect(parseEnvText(envToText(env))).toEqual({ A: "line1" });
  });
});
