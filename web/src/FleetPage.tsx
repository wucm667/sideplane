import { useEffect, useState } from 'react'
import type { NodeState, NodeStatus } from './types.ts'

function stateBadgeClasses(state: NodeState): string {
  switch (state) {
    case 'fresh':
      return 'bg-green-100 text-green-800 border-green-200'
    case 'stale':
      return 'bg-yellow-100 text-yellow-800 border-yellow-200'
    case 'offline':
      return 'bg-red-100 text-red-800 border-red-200'
    default:
      return 'bg-gray-100 text-gray-800 border-gray-200'
  }
}

function formatDate(iso: string | undefined): string {
  if (!iso) return '—'
  try {
    const d = new Date(iso)
    return d.toLocaleString()
  } catch {
    return iso
  }
}

export default function FleetPage() {
  const [nodes, setNodes] = useState<NodeStatus[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const res = await fetch('/api/nodes')
        if (!res.ok) {
          throw new Error(`HTTP ${res.status}: ${res.statusText}`)
        }
        const data: NodeStatus[] = await res.json()
        if (!cancelled) {
          setNodes(data)
          setError(null)
        }
      } catch (e) {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : 'Unknown error')
        }
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  if (loading) {
    return <div className="text-sm text-gray-500">Loading…</div>
  }

  if (error) {
    return (
      <div className="rounded border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">
        Failed to load nodes: {error}
      </div>
    )
  }

  if (!nodes || nodes.length === 0) {
    return (
      <div className="rounded border border-gray-200 bg-white px-4 py-8 text-center text-sm text-gray-500">
        No nodes registered yet.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {nodes.map((node) => (
        <div
          key={node.nodeId}
          className="rounded border border-gray-200 bg-white"
        >
          <div className="flex items-center justify-between border-b border-gray-100 px-4 py-3">
            <div className="flex items-center gap-3">
              <span className="font-mono text-sm font-medium text-gray-900">
                {node.nodeId}
              </span>
              <span
                className={`inline-flex rounded border px-2 py-0.5 text-xs font-medium ${stateBadgeClasses(node.state)}`}
              >
                {node.state}
              </span>
            </div>
            <div className="text-xs text-gray-500">
              {node.sidecarVersion ? `v${node.sidecarVersion}` : '—'}
            </div>
          </div>

          <div className="px-4 py-3">
            <div className="grid grid-cols-2 gap-2 text-sm sm:grid-cols-4">
              <div>
                <div className="text-xs text-gray-500">Hostname</div>
                <div className="font-medium text-gray-900">
                  {node.hostname || '—'}
                </div>
              </div>
              <div>
                <div className="text-xs text-gray-500">Last heartbeat</div>
                <div className="font-medium text-gray-900">
                  {formatDate(node.lastHeartbeatAt)}
                </div>
              </div>
              <div>
                <div className="text-xs text-gray-500">Config hash</div>
                <div className="font-mono text-xs text-gray-900">
                  {node.configHash || '—'}
                </div>
              </div>
              <div>
                <div className="text-xs text-gray-500">Last error</div>
                <div className="text-xs text-gray-900">
                  {node.lastError || '—'}
                </div>
              </div>
            </div>
          </div>

          {node.runtimes && node.runtimes.length > 0 && (
            <div className="border-t border-gray-100 px-4 py-3">
              <div className="mb-2 text-xs font-medium uppercase tracking-wider text-gray-500">
                Runtimes
              </div>
              <div className="space-y-2">
                {node.runtimes.map((rt) => (
                  <div
                    key={rt.name}
                    className="rounded bg-gray-50 px-3 py-2 text-sm"
                  >
                    <div className="flex items-center gap-2">
                      <span className="font-mono font-medium text-gray-900">
                        {rt.name}
                      </span>
                      {rt.type && (
                        <span className="text-xs text-gray-500">({rt.type})</span>
                      )}
                      {rt.state && (
                        <span className="rounded bg-gray-200 px-1.5 py-0.5 text-xs text-gray-700">
                          {rt.state}
                        </span>
                      )}
                    </div>
                    <div className="mt-1 grid grid-cols-2 gap-2 text-xs sm:grid-cols-4">
                      <div>
                        <span className="text-gray-500">Provider:</span>{' '}
                        <span className="text-gray-900">{rt.provider || '—'}</span>
                      </div>
                      <div>
                        <span className="text-gray-500">Model:</span>{' '}
                        <span className="text-gray-900">{rt.model || '—'}</span>
                      </div>
                      <div>
                        <span className="text-gray-500">Hash:</span>{' '}
                        <span className="font-mono text-gray-900">
                          {rt.configHash || '—'}
                        </span>
                      </div>
                      <div>
                        <span className="text-gray-500">Error:</span>{' '}
                        <span className="text-gray-900">{rt.lastError || '—'}</span>
                      </div>
                    </div>
                    {rt.version && (
                      <div className="mt-1 text-xs text-gray-500">
                        Version: {rt.version}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      ))}
    </div>
  )
}
