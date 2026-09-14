import { describe, expect, it } from 'vitest'
import { validServiceEndpoint } from './serviceEndpoint'

describe('service base addresses', () => {
  it('accepts defaults, HTTPS paths and local HTTP', () => {
    for (const value of [
      '',
      'https://yotta.yuelili.com/api/hub',
      'http://localhost:8094',
      'http://127.0.0.1:8090',
      'http://[::1]:8090',
    ])
      expect(validServiceEndpoint(value)).toBe(true)
  })
  it('rejects credentials, query strings, fragments and invalid addresses before saving', () => {
    for (const value of [
      '/api/hub',
      'https://u:p@example.com',
      'https://example.com?',
      'https://example.com/#x',
      'http://example.com',
      'http://localhost:65536',
      'javascript:void(0)',
    ])
      expect(validServiceEndpoint(value)).toBe(false)
  })
})
