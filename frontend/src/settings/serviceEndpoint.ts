export const DEFAULT_SERVICE_ADDRESSES = {
  hubURL: 'https://yotta.yuelili.com/api/hub',
  registryURL: 'https://yotta.yuelili.com/api/registry',
}

export function validServiceEndpoint(value: string): boolean {
  const raw = value.trim()
  if (!raw) return true
  if (raw.length > 2048 || !/^https?:\/\//i.test(raw)) return false
  try {
    const url = new URL(raw)
    if (url.username || url.password || raw.includes('?') || raw.includes('#')) return false
    if (url.port && (Number(url.port) < 1 || Number(url.port) > 65535)) return false
    return url.protocol === 'https:' || ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  } catch {
    return false
  }
}
