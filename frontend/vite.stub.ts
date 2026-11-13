import path from "node:path"
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// 视觉回归桩测试装置：在普通浏览器中通过预设的 IPC 传输提供真实应用
// UI。运行方式： npx vite --config vite.stub.ts --port 9246
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: 9246,
    strictPort: true,
  },
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
});
