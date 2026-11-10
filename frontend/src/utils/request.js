import axios from 'axios'
import { ElMessage } from 'element-plus'

// 所有接口统一走 /api 前缀，由 Vite 代理转发并去掉前缀
const request = axios.create({
  baseURL: '/api',
  timeout: 10000
})

// 请求拦截器：自动携带 JWT
request.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

// 401 统一处理：清除本地登录态并跳回登录页
async function redirectToLogin() {
  const { default: router } = await import('../router')
  const current = router.currentRoute.value
  if (current.path === '/login') return

  localStorage.removeItem('token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('user_id')
  localStorage.removeItem('user_name')
  ElMessage.error('登录已过期，请重新登录')
  router.replace({ path: '/login', query: { redirect: current.fullPath } })
}

// 响应拦截器：
// 成功时直接返回后端统一结构 { code, message, data }
// 失败时按 HTTP 状态码兜底处理
request.interceptors.response.use(
  (response) => {
    const res = response.data
    if (res && typeof res === 'object' && 'code' in res && res.code !== 200) {
      ElMessage.error(res.message || '请求失败')
      return Promise.reject(new Error(res.message || '请求失败'))
    }
    return res
  },
  (error) => {
    const status = error.response?.status
    const message = error.response?.data?.message || error.message || '网络异常'

    if (status === 401) {
      // 登录页上的 401 是「用户名或密码错误」，直接提示，不跳转
      if (window.location.pathname === '/login') {
        ElMessage.error(message)
      } else {
        redirectToLogin()
      }
    } else {
      ElMessage.error(message)
    }

    return Promise.reject(error)
  }
)

export default request
