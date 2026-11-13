/**
 * parseEnvText 把编辑器中的 "KEY=VALUE" 行转换为配置映射，仅在第一处 '='
 * 拆分：值通常包含 '='（base64 填充、连接字符串），而 split("=", 2)
 * 过去会破坏它们。
 */
export function parseEnvText(text: string): Record<string, string> {
  return Object.fromEntries(
    text
      .split("\n")
      .map((l) => {
        const i = l.indexOf("=");
        return i > 0 ? [l.slice(0, i).trim(), l.slice(i + 1)] : null;
      })
      .filter((p): p is [string, string] => !!p && p[0].trim().length > 0),
  );
}

/**
 * envToText 是 parseEnvText 的逆操作，用于编辑器的 textarea。
 * 注意：包含换行的值无法在基于行的编辑器中表示（导入的配置可能含有这类值）——保
 * 存时，parseEnvText 会丢弃第一个换行之后的部分。
 */
export function envToText(env: Record<string, string> | undefined | null): string {
  return Object.entries(env ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");
}
