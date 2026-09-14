import { describe, it, expect } from 'vitest'
import { priceCents, publicationSales, publicationVersion, type SalesDraft } from './publication'
describe('publication sales', () => {
  it('allows rejected versions to be corrected without consuming another version', () => {
    expect(publicationVersion('', '1.0.0')).toBe('1.0.0')
    expect(publicationVersion('1.0.0', '1.1.0')).toBe('1.1.0')
    expect(publicationVersion('2.0.0', '1.1.0')).toBe('2.0.1')
    expect(publicationVersion('1.0.0')).toBe('1.0.1')
  })
  it('converts decimal prices exactly and rejects invalid amounts', () => {
    expect(priceCents('12.34')).toBe(1234)
    expect(priceCents('0.29')).toBe(29)
    for (const value of ['0', '-1', '1.001', '1e2', '', '21474836.48'])
      expect(priceCents(value)).toBeNull()
    expect(priceCents('21474836.47')).toBe(2147483647)
  })
  it('submits paid pricing and preserves current revision', () => {
    const draft: SalesDraft = {
      paid: true,
      price: '12.34',
      revision: 0,
      available: true,
      configured: false,
    }
    expect(publicationSales(draft)).toEqual({
      priceCents: 1234,
      currency: 'CNY',
      revision: 0,
      available: true,
    })
    expect(publicationSales({ ...draft, paid: false })).toBeUndefined()
    expect(
      publicationSales({ ...draft, paid: false, configured: true, revision: 5 }),
    ).toMatchObject({ priceCents: 0, revision: 5 })
  })
})
