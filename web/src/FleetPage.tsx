import { useEffect } from 'react'
import { ActivityView } from './components/ActivityView.tsx'
import { EnrollmentView } from './components/EnrollmentView.tsx'
import { FleetOverview } from './components/FleetOverview.tsx'
import { NodeDetailView } from './components/NodeDetailView.tsx'
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
    loading,
    loadMoreNodeJobs,
    nodes,
    operatorToken,
    openNode,
    refreshFleet,
    refreshSelectedNodeAfterApply,
    refreshing,
    rollingBackByNode,
    saveNodeLabels,
    savingLabelsByNode,
    restartingByNode,
    selectedNode,
    selector,
    setAuditFilters,
    setAuditLimit,
    setNodeJobStatusFilter,
    setOperatorToken,
    setSelector,
    stats,
    theme,
    toggleTheme,
    view,
    loadAuditEvents,
  } = useFleetPageController()

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
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
      if (key === 'r') {
        event.preventDefault()
        if (view === 'activity') {
          void loadAuditEvents()
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
  }, [changeView, loadAuditEvents, refreshFleet, view])

  return (
    <div data-sideplane-theme={theme} className="min-h-screen bg-[var(--sp-bg)] text-[var(--sp-text)]">
      <div className="flex min-h-screen flex-col md:flex-row">
        <Sidebar
          currentView={view}
          groups={groups}
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
              refreshing={refreshing}
              selector={selector}
              stats={stats}
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
              operatorToken={operatorToken}
              onBack={() => changeView('fleet')}
              onDeepProbe={() => createDeepProbe(selectedNode.nodeId)}
              onRollback={(request) => createRollback(selectedNode.nodeId, request)}
              onRestart={(request) => createRestart(selectedNode.nodeId, request)}
              onJobStatusFilterChange={(status) => setNodeJobStatusFilter(selectedNode.nodeId, status)}
              onLoadMoreJobs={() => loadMoreNodeJobs(selectedNode.nodeId)}
              onSaveLabels={(labels) => saveNodeLabels(selectedNode.nodeId, labels)}
              onApplied={refreshSelectedNodeAfterApply}
            />
          )}
          {view === 'node' && !selectedNode && (
            <EmptyState title="Node not found" body="Return to Fleet and select a registered node." />
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
