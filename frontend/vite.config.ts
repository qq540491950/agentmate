import path from "node:path"
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    // 9527: 与 Taskfile.yml 的 VITE_PORT 默认值保持一致（避开 9245 冲突）
    port: Number(process.env.WAILS_VITE_PORT) || 9527,
    strictPort: true,
  },
  plugins: [react(), tailwindcss(), wails("./bindings")],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
    },
  },
  build: {
    // 将第三方库从应用 chunk 中拆分出来：React 和 markdown 渲染器很少变更，
    // 因此它们可以跨应用更新被缓存，而不是每次构建都重新下载。Rolldown（Vite
    // 8） 仅接受函数形式。
    rollupOptions: {
      output: {
        manualChunks(id: string) {
          if (!id.includes("node_modules")) return undefined;
          if (/node_modules\/(react|react-dom|scheduler)\//.test(id)) return "react";
          if (/node_modules\/(react-markdown|remark-|micromark|mdast-|hast-|unist-|unified|vfile)/.test(id)) return "markdown";
          return undefined;
        },
      },
    },
  },
});
