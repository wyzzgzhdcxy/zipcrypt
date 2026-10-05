import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import wails from "@wailsio/runtime/plugins/vite";
import AutoImport from "unplugin-auto-import/vite";
import Components from "unplugin-vue-components/vite";
import { ElementPlusResolver } from "unplugin-vue-components/resolvers";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9246,
    strictPort: true,
  },
  plugins: [
    vue(),
    // 自动生成 frontend/bindings 下的 TS 绑定
    wails("./bindings"),
    // Element Plus 按需导入：模板里的 el-* 组件与 ElMessage/ElMessageBox
    // 自动引入（含样式），避免整包打进产物
    AutoImport({
      resolvers: [ElementPlusResolver()],
      dts: "src/auto-imports.d.ts",
    }),
    Components({
      resolvers: [ElementPlusResolver()],
      dts: "src/components.d.ts",
    }),
  ],
  build: {
    // 本地桌面应用，首屏加载的是内嵌资源，不需要为体积分包
    chunkSizeWarningLimit: 1500,
  },
});
