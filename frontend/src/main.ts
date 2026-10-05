import { createApp } from 'vue'

import App from './App.vue'
import './style.css'

// Element Plus 的 el-* 组件与 ElMessage/ElMessageBox 由
// unplugin-vue-components / unplugin-auto-import 按需自动注册（含样式），
// 避免整包打进产物，因此这里不显式 import。
createApp(App).mount('#app')
