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
    auditLoading,
    bannerText,
    changeView,
    createDeepProbe,
    creatingByNode,
    effectiveByNode,
    effectiveErrorByNode,
    error,
    fleetSubtitle,
    groups,
    jobsByNode,
    jobsErrorByNode,
    jobsLoadingByNode,
    loading,
    nodes,
    operatorToken,
    openNode,
    refreshFleet,
    refreshSelectedNodeAfterApply,
    refreshing,
    selectedNode,
    setAuditFilters,
    setOperatorToken,
    stats,
    theme,
    toggleTheme,
    view,
    loadAuditEvents,
  } = useFleetPageController()

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
              stats={stats}
              onOpenNode={openNode}
              onRefresh={() => refreshFleet()}
            />
          )}
          {view === 'node' && selectedNode && (
            <NodeDetailView
              creating={Boolean(creatingByNode[selectedNode.nodeId])}
              jobs={jobsByNode[selectedNode.nodeId] ?? []}
              jobsError={jobsErrorByNode[selectedNode.nodeId]}
              jobsLoading={Boolean(jobsLoadingByNode[selectedNode.nodeId])}
              node={selectedNode}
              effective={effectiveByNode[selectedNode.nodeId]}
              effectiveError={effectiveErrorByNode[selectedNode.nodeId]}
              operatorToken={operatorToken}
              onBack={() => changeView('fleet')}
              onDeepProbe={() => createDeepProbe(selectedNode.nodeId)}
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
              loading={auditLoading}
              onFiltersChange={setAuditFilters}
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

function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="mx-auto max-w-3xl px-4 py-12 text-center sm:px-6 lg:px-9">
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="mt-2 text-sm text-[var(--sp-muted)]">{body}</p>
    </div>
  )
}
