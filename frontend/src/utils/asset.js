// 后端返回的封面/视频/头像地址都是相对路径（如 /upload/covers/...、/static/...）。
// 生产环境走同源：Nginx 把 /upload/ 直接映射到共享卷，/static/ 反代到 Go，
// 因此默认返回相对路径即可，不能硬编码 http://127.0.0.1:8080
// （Docker 里 backend-api 没有映射宿主机端口，浏览器会连接失败导致视频黑屏）。
//
// 如需在特殊环境下直连其他资源域名，可通过 VITE_ASSET_BASE 覆盖，例如：
//   VITE_ASSET_BASE=http://127.0.0.1:8080
const ASSET_BASE = (import.meta.env.VITE_ASSET_BASE || '').replace(/\/+$/, '')

export function resolveAsset(url) {
  if (!url) return ''
  if (/^https?:\/\//i.test(url)) return url
  const path = `${url.startsWith('/') ? '' : '/'}${url}`
  return `${ASSET_BASE}${path}`
}
