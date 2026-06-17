import { useCallback, useEffect, useRef, useState } from 'react'
import type { Job, JobStatus, NodeState, NodeStatus } from './types.ts'

const NODE_REFRESH_MS = 10_000
const ACTIVE_JOB_REFRESH_MS = 2_000
const ACTIVE_JOB_STATUSES: JobStatus[] = ['pending', 'claimed']

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

function jobBadgeClasses(status: JobStatus): string {
  switch (status) {
    case 'pending':
      return 'bg-gray-100 text-gray-800 border-gray-200'
    case 'claimed':
      return 'bg-blue-100 text-blue-800 border-blue-200'
    case 'completed':
      return 'bg-green-100 text-green-800 border-green-200'
    case 'failed':
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

function hasActiveJobs(jobs: Job[]): boolean {
  return jobs.some((job) => ACTIVE_JOB_STATUSES.includes(job.status))
}

export default function FleetPage() {
  const [nodes, setNodes] = useState<NodeStatus[] | null>(null)
  const [jobsByNode, setJobsByNode] = useState<Record<string, Job[]>>({})
  const [jobsLoadingByNode, setJobsLoadingByNode] = useState<Record<string, boolean>>({})
  const [jobsErrorByNode, setJobsErrorByNode] = useState<Record<string, string>>({})
  const [creatingByNode, setCreatingByNode] = useState<Record<string, boolean>>({})
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(false)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  const loadNodeJobs = useCallback(async (nodeId: string, showLoading = true) => {
    if (!mountedRef.current) return
    if (showLoading) {
      setJobsLoadingByNode((current) => ({ ...current, [nodeId]: true }))
    }
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/jobs`)
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${res.statusText}`)
      }
      const data: Job[] = await res.json()
      if (!mountedRef.current) return
      setJobsByNode((current) => ({ ...current, [nodeId]: data }))
      setJobsErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
    } catch (e) {
      if (!mountedRef.current) return
      setJobsErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    } finally {
      if (mountedRef.current && showLoading) {
        setJobsLoadingByNode((current) => ({ ...current, [nodeId]: false }))
      }
    }
  }, [])

  const loadNodes = useCallback(async () => {
    const res = await fetch('/api/nodes')
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    }
    const data: NodeStatus[] = await res.json()
    if (!mountedRef.current) return null
    setNodes(data)
    setError(null)
    return data
  }, [])

  const refreshFleet = useCallback(async (showRefreshing = true) => {
    if (!mountedRef.current) return
    if (showRefreshing) {
      setRefreshing(true)
    }
    try {
      const data = await loadNodes()
      if (data) {
        await Promise.all(data.map((node) => loadNodeJobs(node.nodeId, showRefreshing)))
      }
    } catch (e) {
      if (mountedRef.current) {
        setError(e instanceof Error ? e.message : 'Unknown error')
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false)
        if (showRefreshing) {
          setRefreshing(false)
        }
      }
    }
  }, [loadNodeJobs, loadNodes])

  const createDeepProbe = useCallback(async (nodeId: string) => {
    if (!mountedRef.current) return
    setCreatingByNode((current) => ({ ...current, [nodeId]: true }))
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/jobs`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: 'deep_probe' }),
      })
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${res.statusText}`)
      }
      const job: Job = await res.json()
      if (!mountedRef.current) return
      setJobsByNode((current) => ({
        ...current,
        [nodeId]: [job, ...(current[nodeId] ?? []).filter((item) => item.id !== job.id)],
      }))
      setJobsErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
      await loadNodeJobs(nodeId, false)
    } catch (e) {
      if (!mountedRef.current) return
      setJobsErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    } finally {
      if (mountedRef.current) {
        setCreatingByNode((current) => ({ ...current, [nodeId]: false }))
      }
    }
  }, [loadNodeJobs])

  useEffect(() => {
    refreshFleet(false)
  }, [refreshFleet])

  useEffect(() => {
    const interval = window.setInterval(() => {
      refreshFleet(false)
    }, NODE_REFRESH_MS)
    return () => window.clearInterval(interval)
  }, [refreshFleet])

  useEffect(() => {
    const nodeIdsWithActiveJobs = Object.entries(jobsByNode)
      .filter(([, jobs]) => hasActiveJobs(jobs))
      .map(([nodeId]) => nodeId)

    if (nodeIdsWithActiveJobs.length === 0) return

    const interval = window.setInterval(() => {
      void Promise.all(nodeIdsWithActiveJobs.map((nodeId) => loadNodeJobs(nodeId, false)))
    }, ACTIVE_JOB_REFRESH_MS)

    return () => window.clearInterval(interval)
  }, [jobsByNode, loadNodeJobs])

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
      <div className="space-y-4">
        <div className="flex justify-end">
          <button
            type="button"
            className="rounded border border-gray-300 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60"
            disabled={refreshing}
            onClick={() => refreshFleet()}
          >
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>
        <div className="rounded border border-gray-200 bg-white px-4 py-8 text-center text-sm text-gray-500">
          No nodes registered yet.
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <button
          type="button"
          className="rounded border border-gray-300 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60"
          disabled={refreshing}
          onClick={() => refreshFleet()}
        >
          {refreshing ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      {nodes.map((node) => {
        const jobs = jobsByNode[node.nodeId] ?? []
        const jobsLoading = jobsLoadingByNode[node.nodeId]
        const jobsError = jobsErrorByNode[node.nodeId]
        const creating = creatingByNode[node.nodeId]

        return (
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
              <div className="flex items-center gap-3">
                <button
                  type="button"
                  className="rounded border border-gray-300 bg-white px-3 py-1 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60"
                  disabled={creating}
                  onClick={() => createDeepProbe(node.nodeId)}
                >
                  {creating ? 'Creating…' : 'Deep Probe'}
                </button>
                <div className="text-xs text-gray-500">
                  {node.sidecarVersion ? `v${node.sidecarVersion}` : '—'}
                </div>
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

            <div className="border-t border-gray-100 px-4 py-3">
              <div className="mb-2 flex items-center justify-between">
                <div className="text-xs font-medium uppercase tracking-wider text-gray-500">
                  Recent jobs
                </div>
                {jobsLoading && <div className="text-xs text-gray-500">Loading jobs…</div>}
              </div>
              {jobsError && (
                <div className="mb-2 rounded border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-800">
                  Failed to load jobs: {jobsError}
                </div>
              )}
              {jobs.length === 0 ? (
                <div className="rounded bg-gray-50 px-3 py-2 text-sm text-gray-500">
                  No jobs yet.
                </div>
              ) : (
                <div className="overflow-x-auto">
                  <table className="min-w-full text-left text-xs">
                    <thead className="text-gray-500">
                      <tr>
                        <th className="py-2 pr-3 font-medium">ID</th>
                        <th className="py-2 pr-3 font-medium">Type</th>
                        <th className="py-2 pr-3 font-medium">Status</th>
                        <th className="py-2 pr-3 font-medium">Created</th>
                        <th className="py-2 pr-3 font-medium">Claimed</th>
                        <th className="py-2 pr-3 font-medium">Finished</th>
                        <th className="py-2 font-medium">Error</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-100">
                      {jobs.map((job) => (
                        <tr key={job.id}>
                          <td className="py-2 pr-3 font-mono text-gray-900">{job.id}</td>
                          <td className="py-2 pr-3 font-mono text-gray-900">{job.type}</td>
                          <td className="py-2 pr-3">
                            <span className={`inline-flex rounded border px-2 py-0.5 font-medium ${jobBadgeClasses(job.status)}`}>
                              {job.status}
                            </span>
                          </td>
                          <td className="py-2 pr-3 text-gray-700">{formatDate(job.createdAt)}</td>
                          <td className="py-2 pr-3 text-gray-700">{formatDate(job.claimedAt)}</td>
                          <td className="py-2 pr-3 text-gray-700">{formatDate(job.finishedAt)}</td>
                          <td className="py-2 text-gray-700">{job.error || '—'}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </div>
        )
      })}
    </div>
  )
}
