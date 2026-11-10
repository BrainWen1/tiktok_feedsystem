// 后端返回的封面/视频地址是相对路径（如 /upload/covers/...），
// 浏览器需要直连后端资源域名，否则会请求 Vite 服务器导致 404。
const ASSET_BASE = 'http://127.0.0.1:8080'

export function resolveAsset(url) {
  if (!url) return ''
  if (/^https?:\/\//i.test(url)) return url
  return `${ASSET_BASE}${url.startsWith('/') ? '' : '/'}${url}`
}
