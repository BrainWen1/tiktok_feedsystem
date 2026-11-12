import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
// 必须在浅色样式之后引入，html.dark 下的 Element Plus 变量才能覆盖生效
import 'element-plus/theme-chalk/dark/css-vars.css'

import App from './App.vue'
import router from './router'
import { initTheme } from './utils/theme'
import './style.css'

// 尽早应用主题，避免刷新后先闪一下浅色再切到深色
initTheme()

const app = createApp(App)

app.use(createPinia())
app.use(ElementPlus, { locale: zhCn })
app.use(router)
app.mount('#app')
