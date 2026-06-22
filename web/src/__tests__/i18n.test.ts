import { describe, expect, it } from 'vitest'
import { translations, translate } from '../i18n.ts'

describe('i18n', () => {
  it('looks up Chinese strings', () => {
    expect(translate('zh', 'fleet.title')).toBe('集群')
  })

  it('falls back to English and then to the key', () => {
    const fallbackKey = '__test.enOnly'
    translations.en[fallbackKey] = 'English fallback'
    const previousZh = translations.zh[fallbackKey]
    delete translations.zh[fallbackKey]

    try {
      expect(translate('zh', fallbackKey)).toBe('English fallback')
      expect(translate('zh', '__test.missing')).toBe('__test.missing')
    } finally {
      delete translations.en[fallbackKey]
      if (previousZh !== undefined) {
        translations.zh[fallbackKey] = previousZh
      }
    }
  })

  it('interpolates params', () => {
    expect(translate('en', 'command.openNode', { nodeId: 'node-a' })).toBe('Open node node-a')
    expect(translate('zh', 'command.openNode', { nodeId: 'node-a' })).toBe('打开节点 node-a')
  })

  it('keeps en and zh key sets in parity', () => {
    const enKeys = Object.keys(translations.en).sort()
    const zhKeys = Object.keys(translations.zh).sort()

    expect(zhKeys).toEqual(enKeys)
  })
})
