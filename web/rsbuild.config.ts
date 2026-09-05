import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
import tailwindcss from "@tailwindcss/postcss";

const apiTarget = process.env.MANYROUTER_API_TARGET ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [pluginReact()],
  source: { entry: { index: "./src/main.tsx" } },
  html: {
    template: "./index.html",
    title: "ManyRouter",
    lang: "zh-CN",
    favicon: "./public/favicon.svg",
  },
  server: {
    port: 3000,
    proxy: { "/api": { target: apiTarget, changeOrigin: false } },
  },
  output: { cleanDistPath: true },
  tools: { postcss: { postcssOptions: { plugins: [tailwindcss()] } } },
});
