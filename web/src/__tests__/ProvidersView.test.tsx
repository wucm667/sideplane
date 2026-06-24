import type { JSXElementConstructor, ReactElement, ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const useStateMock = vi.hoisted(() => vi.fn())

vi.mock('react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react')>()
  return {
    ...actual,
    useState: useStateMock,
  }
})

vi.mock('../i18n.ts', () => ({
  useT: () => ({
    lang: 'en',
    setLang: () => undefined,
    t: (key: string, params?: Record<string, string | number | boolean | null | undefined>) => translateForTest(key, params),
  }),
}))

import { ProviderForm, ProvidersView } from '../components/ProvidersView.tsx'
import type { ProviderDefinition } from '../types.ts'

type TestElement = ReactElement<{ children?: ReactNode; [key: string]: unknown }, string | JSXElementConstructor<unknown>>

describe('ProvidersView', () => {
  beforeEach(() => {
    useStateMock.mockReset()
    useStateMock.mockImplementation((initial: unknown) => [initial, vi.fn()])
  })

  it('renders provider catalog rows', () => {
    const element = ProvidersView({
      providers: [
        {
          name: 'openai',
          baseURL: 'https://api.openai.com/v1',
          models: ['gpt-4o', 'gpt-4o-mini'],
          apiKeyEnv: 'OPENAI_API_KEY',
          apiKeyManaged: true,
        },
      ],
      loading: false,
      error: null,
      saving: false,
      onRefresh: vi.fn(),
      onUpsert: vi.fn(),
      onDelete: vi.fn(),
    })

    const text = textContent(element)

    expect(text).toContain('openai')
    expect(text).toContain('https://api.openai.com/v1')
    expect(text).toContain('gpt-4o, gpt-4o-mini')
    expect(text).toContain('OPENAI_API_KEY')
    expect(text).toContain('Key stored')
  })

  it('calls onUpsert from the add form with parsed provider fields and api key', async () => {
    const onUpsert = vi.fn().mockResolvedValue(true)
    const setAPIKey = vi.fn()
    useStateMock
      .mockImplementationOnce(() => ['openai', vi.fn()])
      .mockImplementationOnce(() => [' https://api.openai.com/v1 ', vi.fn()])
      .mockImplementationOnce(() => ['gpt-4o\ngpt-4o-mini', vi.fn()])
      .mockImplementationOnce(() => [' OPENAI_API_KEY ', vi.fn()])
      .mockImplementationOnce(() => [' sk-test ', setAPIKey])

    const element = ProviderForm({
      provider: null,
      saving: false,
      onCancel: vi.fn(),
      onSubmit: onUpsert,
    })
    const form = findOne(element, (item) => item.type === 'form')

    await (form.props.onSubmit as (event: { preventDefault: () => void }) => void | Promise<void>)({ preventDefault: vi.fn() })

    expect(onUpsert).toHaveBeenCalledWith({
      name: 'openai',
      baseURL: 'https://api.openai.com/v1',
      models: ['gpt-4o', 'gpt-4o-mini'],
      apiKeyEnv: 'OPENAI_API_KEY',
    }, ' sk-test ')
    expect(setAPIKey).toHaveBeenCalledWith('')
  })

  it('omits apiKey from onUpsert when the add form key field is blank', async () => {
    const onUpsert = vi.fn().mockResolvedValue(true)
    useStateMock
      .mockImplementationOnce(() => ['openai', vi.fn()])
      .mockImplementationOnce(() => [' https://api.openai.com/v1 ', vi.fn()])
      .mockImplementationOnce(() => ['gpt-4o\ngpt-4o-mini', vi.fn()])
      .mockImplementationOnce(() => [' OPENAI_API_KEY ', vi.fn()])
      .mockImplementationOnce(() => ['', vi.fn()])

    const element = ProviderForm({
      provider: null,
      saving: false,
      onCancel: vi.fn(),
      onSubmit: onUpsert,
    })
    const form = findOne(element, (item) => item.type === 'form')

    await (form.props.onSubmit as (event: { preventDefault: () => void }) => void | Promise<void>)({ preventDefault: vi.fn() })

    expect(onUpsert.mock.calls[0]).toEqual([{
      name: 'openai',
      baseURL: 'https://api.openai.com/v1',
      models: ['gpt-4o', 'gpt-4o-mini'],
      apiKeyEnv: 'OPENAI_API_KEY',
    }])
  })

  it('asks for delete confirmation before calling onDelete', async () => {
    const provider: ProviderDefinition = {
      name: 'openai',
      models: ['gpt-4o'],
      apiKeyEnv: 'OPENAI_API_KEY',
    }
    const onDelete = vi.fn()
    useStateMock
      .mockImplementationOnce((initial: unknown) => [initial, vi.fn()])
      .mockImplementationOnce((initial: unknown) => [initial, vi.fn()])
      .mockImplementationOnce(() => ['openai', vi.fn()])

    const element = ProvidersView({
      providers: [provider],
      loading: false,
      error: null,
      saving: false,
      onRefresh: vi.fn(),
      onUpsert: vi.fn(),
      onDelete,
    })
    const dialog = findOne(element, (item) => item.props.role === 'dialog')
    const deleteButton = findAll(dialog, (item) => item.type === 'button')
      .find((item) => textContent(item) === 'Delete')

    expect(textContent(dialog)).toContain('Delete provider openai?')
    expect(onDelete).not.toHaveBeenCalled()

    await (deleteButton?.props.onClick as (() => void | Promise<void>))()

    expect(onDelete).toHaveBeenCalledWith('openai')
  })
})

function translateForTest(key: string, params?: Record<string, string | number | boolean | null | undefined>): string {
  const translations: Record<string, string> = {
    'common.saving': 'Saving...',
    'providers.add': 'Add provider',
    'providers.apiKey': 'API key',
    'providers.apiKeyEnv': 'API key env var',
    'providers.apiKeyEnvHint': 'Environment variable name only — set the real key in the node\'s ~/.hermes/.env. Sideplane never stores plaintext keys.',
    'providers.apiKeyEnvOnly': 'Env reference',
    'providers.apiKeyHint': 'Optional; stored encrypted and pushed to nodes on apply. Leave blank to reference an existing env var above.',
    'providers.apiKeyKeepHint': 'A key is already stored. Leave blank to keep it, or enter a new key to replace it.',
    'providers.apiKeyManagedBadge': 'Key stored',
    'providers.baseURL': 'Base URL',
    'providers.cancel': 'Cancel',
    'providers.confirmDelete': 'Delete provider {name}?',
    'providers.delete': 'Delete',
    'providers.edit': 'Edit provider',
    'providers.empty': 'No providers in the global catalog yet.',
    'providers.error': 'Provider catalog error: {error}',
    'providers.loading': 'Loading providers...',
    'providers.models': 'Models',
    'providers.modelsHint': 'One model ID per line.',
    'providers.name': 'Name',
    'providers.refresh': 'Refresh',
    'providers.save': 'Save provider',
    'providers.subtitle': 'Manage the global provider catalog used by desired configuration.',
    'providers.title': 'Providers',
  }
  const template = translations[key] ?? key
  return template.replace(/\{([a-zA-Z0-9_]+)\}/g, (match, name: string) => {
    const value = params?.[name]
    return value === null || value === undefined ? match : String(value)
  })
}

function isElement(node: ReactNode): node is TestElement {
  return typeof node === 'object' && node !== null && 'props' in node
}

function childrenOf(node: TestElement): ReactNode[] {
  const children = node.props.children
  return Array.isArray(children) ? children : [children]
}

function textContent(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(textContent).join('')
  if (!isElement(node)) return ''
  return childrenOf(node).map(textContent).join('')
}

function findAll(node: ReactNode, predicate: (element: TestElement) => boolean): TestElement[] {
  if (Array.isArray(node)) return node.flatMap((child) => findAll(child, predicate))
  if (!isElement(node)) return []

  const matches = predicate(node) ? [node] : []
  return matches.concat(childrenOf(node).flatMap((child) => findAll(child, predicate)))
}

function findOne(node: ReactNode, predicate: (element: TestElement) => boolean): TestElement {
  const match = findAll(node, predicate)[0]
  if (!match) {
    throw new Error('Expected element was not found')
  }
  return match
}
