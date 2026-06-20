import { useEffect, useMemo, useState } from 'react'
import { ActivityView } from './components/ActivityView.tsx'
import { CommandPalette, type CommandItem } from './components/CommandPalette.tsx'
import { EnrollmentView } from './components/EnrollmentView.tsx'
import { FleetOverview } from './components/FleetOverview.tsx'
import { NodeDetailView } from './components/NodeDetailView.tsx'
import { RolloutsView } from './components/RolloutsView.tsx'
import { Sidebar } from './components/Sidebar.tsx'
import { useFleetPageController } from './helpers.ts'

export default function FleetPage() {
  const {
    auditError,
    auditEvents,
    auditFilters,
    auditLimit,
    auditLoading,
    backupsByNode,
    backupsErrorByNode,
    backupsLoadingByNode,
    bannerText,
    changeView,
    createDeepProbe,
    createRollback,
    createRestart,
    createRollout,
    creatingRollout,
    creatingByNode,
    effectiveByNode,
    effectiveErrorByNode,
    error,
    fleetSubtitle,
    groups,
    jobsByNode,
    jobsErrorByNode,
    jobLimitByNode,
    jobsLoadingByNode,
    jobStatusByNode,
    labelErrorByNode,
    liveConnected,
    loading,
    loadMoreNodeJobs,
    loadRollouts,
    maintenanceErrorByNode,
    nodes,
    operatorToken,
    openNode,
    refreshFleet,
    refreshSelectedNodeAfterApply,
    refreshing,
    rolloutActioningId,
    rollouts,
    rolloutsError,
    rolloutsLoading,
    rollingBackByNode,
    saveNodeLabels,
    savingLabelsByNode,
    savingMaintenanceByNode,
    restartingByNode,
    selectedNode,
    selector,
    setAuditFilters,
    setAuditLimit,
    setNodeJobStatusFilter,
    setNodeMaintenance,
    setOperatorToken,
    setSelector,
    theme,
    toggleTheme,
    view,
    loadAuditEvents,
    performRolloutAction,
  } = useFleetPageController()

  const [paletteOpen, setPaletteOpen] = useState(false)

  const commands = useMemo<CommandItem[]>(() => {
    const items: CommandItem[] = [
      { id: 'view:fleet', label: 'Go to Fleet', keywords: 'fleet nodes view', run: () => changeView('fleet') },
      { id: 'view:activity', label: 'Go to Activity', keywords: 'activity audit view', run: () => changeView('activity') },
      { id: 'view:enrollment', label: 'Go to Enrollment & Settings', keywords: 'enrollment tokens webhooks settings view', run: () => changeView('enrollment') },
      { id: 'view:rollouts', label: 'Go to Rollouts', keywords: 'rollouts view', run: () => changeView('rollouts') },
      { id: 'action:new-rollout', label: 'New rollout', keywords: 'create rollout deploy', run: () => changeView('rollouts') },
    ]
    for (const node of nodes) {
      const labels = Object.entries(node.labels ?? {}).map(([key, value]) => `${key}=${value}`).join(' ')
      const keywords = `${node.nodeId} ${node.hostname ?? ''} ${labels}`
      items.push({ id: `open:${node.nodeId}`, label: `Open node ${node.nodeId}`, hint: node.hostname, keywords, run: () => openNode(node.nodeId) })
      items.push({ id: `probe:${node.nodeId}`, label: `Deep probe ${node.nodeId}`, hint: node.hostname, keywords: `probe ${keywords}`, run: () => void createDeepProbe(node.nodeId) })
    }
    return items
  }, [changeView, createDeepProbe, nodes, openNode])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setPaletteOpen((open) => !open)
        return
      }
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return
      if (isEditableTarget(event.target)) return

      const key = event.key.toLowerCase()
      if (key === '1' || key === 'f') {
        event.preventDefault()
        changeView('fleet')
        return
      }
      if (key === '2' || key === 'a') {
        event.preventDefault()
        changeView('activity')
        return
      }
      if (key === '3' || key === 'e') {
        event.preventDefault()
        changeView('enrollment')
        return
      }
      if (key === '4') {
        event.preventDefault()
        changeView('rollouts')
        return
      }
      if (key === 'r') {
        event.preventDefault()
        if (view === 'activity') {
          void loadAuditEvents()
        } else if (view === 'rollouts') {
          void loadRollouts()
        } else {
          void refreshFleet()
        }
        return
      }
      if (key === 'escape' && view === 'node') {
        event.preventDefault()
        changeView('fleet')
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [changeView, loadAuditEvents, loadRollouts, refreshFleet, view])

  return (
    <div data-sideplane-theme={theme} className="min-h-screen bg-[var(--sp-bg)] text-[var(--sp-text)]">
      <div className="flex min-h-screen flex-col md:flex-row">
        <Sidebar
          currentView={view}
          groups={groups}
          liveConnected={liveConnected}
          operatorToken={operatorToken}
          theme={theme}
          onOperatorTokenChange={setOperatorToken}
          onThemeToggle={toggleTheme}
          onViewChange={changeView}
        />

        <main className="min-w-0 flex-1 overflow-y-auto">
          {view === 'fleet' && (
            <FleetOverview
              bannerText={bannerText}
              error={error}
              fleetSubtitle={fleetSubtitle}
              jobsByNode={jobsByNode}
              loading={loading}
              nodes={nodes}
              operatorToken={operatorToken}
              refreshing={refreshing}
              rollouts={rollouts}
              selector={selector}
              onOpenNode={openNode}
              onRefresh={() => refreshFleet()}
              onSelectorChange={setSelector}
            />
          )}
          {view === 'node' && selectedNode && (
            <NodeDetailView
              creating={Boolean(creatingByNode[selectedNode.nodeId])}
              rollingBack={Boolean(rollingBackByNode[selectedNode.nodeId])}
              restarting={Boolean(restartingByNode[selectedNode.nodeId])}
              backups={backupsByNode[selectedNode.nodeId] ?? []}
              backupsError={backupsErrorByNode[selectedNode.nodeId]}
              backupsLoading={Boolean(backupsLoadingByNode[selectedNode.nodeId])}
              jobs={jobsByNode[selectedNode.nodeId] ?? []}
              jobsError={jobsErrorByNode[selectedNode.nodeId]}
              jobLimit={jobLimitByNode[selectedNode.nodeId] ?? 50}
              jobsLoading={Boolean(jobsLoadingByNode[selectedNode.nodeId])}
              jobStatusFilter={jobStatusByNode[selectedNode.nodeId] ?? ''}
              node={selectedNode}
              effective={effectiveByNode[selectedNode.nodeId]}
              effectiveError={effectiveErrorByNode[selectedNode.nodeId]}
              labelError={labelErrorByNode[selectedNode.nodeId]}
              labelsSaving={Boolean(savingLabelsByNode[selectedNode.nodeId])}
              maintenanceError={maintenanceErrorByNode[selectedNode.nodeId]}
              maintenanceSaving={Boolean(savingMaintenanceByNode[selectedNode.nodeId])}
              operatorToken={operatorToken}
              onBack={() => changeView('fleet')}
              onDeepProbe={() => createDeepProbe(selectedNode.nodeId)}
              onRollback={(request) => createRollback(selectedNode.nodeId, request)}
              onRestart={(request) => createRestart(selectedNode.nodeId, request)}
              onJobStatusFilterChange={(status) => setNodeJobStatusFilter(selectedNode.nodeId, status)}
              onLoadMoreJobs={() => loadMoreNodeJobs(selectedNode.nodeId)}
              onMaintenanceChange={(maintenance) => setNodeMaintenance(selectedNode.nodeId, maintenance)}
              onSaveLabels={(labels) => saveNodeLabels(selectedNode.nodeId, labels)}
              onApplied={refreshSelectedNodeAfterApply}
            />
          )}
          {view === 'node' && !selectedNode && (
            <EmptyState title="Node not found" body="Return to Fleet and select a registered node." />
          )}
          {view === 'rollouts' && (
            <RolloutsView
              actioningId={rolloutActioningId}
              creating={creatingRollout}
              error={rolloutsError}
              loading={rolloutsLoading}
              nodes={nodes}
              operatorToken={operatorToken}
              rollouts={rollouts}
              onAction={performRolloutAction}
              onCreate={createRollout}
              onOpenNode={openNode}
              onRefresh={loadRollouts}
            />
          )}
          {view === 'activity' && (
            <ActivityView
              error={auditError}
              events={auditEvents}
              filters={auditFilters}
              limit={auditLimit}
              loading={auditLoading}
              onFiltersChange={setAuditFilters}
              onLimitChange={setAuditLimit}
              onRefresh={loadAuditEvents}
            />
          )}
          {view === 'enrollment' && (
            <EnrollmentView operatorToken={operatorToken} />
          )}
        </main>
      </div>
      <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />
    </div>
  )
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tagName = target.tagName.toLowerCase()
  return tagName === 'input' || tagName === 'textarea' || tagName === 'select'
}

function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="mx-auto max-w-3xl px-4 py-12 text-center sm:px-6 lg:px-9">
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="mt-2 text-sm text-[var(--sp-muted)]">{body}</p>
    </div>
  )
}
