import { useEffect, useMemo, useState } from 'react'
import { ActivityView } from './components/ActivityView.tsx'
import { CommandPalette, type CommandItem } from './components/CommandPalette.tsx'
import { EnrollmentView } from './components/EnrollmentView.tsx'
import { FleetOverview } from './components/FleetOverview.tsx'
import { NodeDetailView } from './components/NodeDetailView.tsx'
import { ProvidersView } from './components/ProvidersView.tsx'
import { RolloutsView } from './components/RolloutsView.tsx'
import { Sidebar } from './components/Sidebar.tsx'
import { useFleetPageController } from './helpers.ts'
import { LanguageProvider, useT } from './i18n.ts'

export default function FleetPage() {
  const controller = useFleetPageController()

  return (
    <LanguageProvider lang={controller.lang} setLang={controller.setLang}>
      <FleetPageContent controller={controller} />
    </LanguageProvider>
  )
}

function FleetPageContent({ controller }: { controller: ReturnType<typeof useFleetPageController> }) {
  const { t } = useT()
  const {
    auditError,
    auditEvents,
    auditFilters,
    auditLimit,
    auditLoading,
    backupsByNode,
    backupsErrorByNode,
    backupsLoadingByNode,
    changeView,
    createDeepProbe,
    createRollback,
    createRestart,
    createRollout,
    creatingRollout,
    creatingByNode,
    deleteProvider,
    effectiveByNode,
    effectiveErrorByNode,
    error,
    groups,
    jobsByNode,
    jobsErrorByNode,
    jobLimitByNode,
    jobsLoadingByNode,
    jobStatusByNode,
    labelErrorByNode,
    lang,
    liveConnected,
    loading,
    loadMoreNodeJobs,
    loadProviders,
    loadRollouts,
    maintenanceErrorByNode,
    nodes,
    operatorToken,
    openNode,
    providers,
    providersError,
    providersLoading,
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
    savingProvider,
    restartingByNode,
    selectedNode,
    selector,
    stats,
    setAuditFilters,
    setAuditLimit,
    setNodeJobStatusFilter,
    setNodeMaintenance,
    setOperatorToken,
    setSelector,
    theme,
    toggleLang,
    toggleTheme,
    view,
    loadAuditEvents,
    performRolloutAction,
    upsertProvider,
  } = controller

  const [paletteOpen, setPaletteOpen] = useState(false)
  const fleetSubtitle = t('fleet.subtitle', { nodes: nodes.length, groups: groups.length, healthy: stats.healthy })
  const bannerText = [
    stats.drift > 0 ? t(stats.drift === 1 ? 'fleet.banner.driftOne' : 'fleet.banner.driftMany', { count: stats.drift }) : '',
    stats.stale > 0 ? t('fleet.banner.stale', { count: stats.stale }) : '',
    stats.offline > 0 ? t('fleet.banner.offline', { count: stats.offline }) : '',
  ].filter(Boolean).join(' · ')

  const commands = useMemo<CommandItem[]>(() => {
    const items: CommandItem[] = [
      { id: 'view:fleet', label: t('command.goFleet'), keywords: t('command.fleetKeywords'), run: () => changeView('fleet') },
      { id: 'view:activity', label: t('command.goActivity'), keywords: t('command.activityKeywords'), run: () => changeView('activity') },
      { id: 'view:enrollment', label: t('command.goEnrollment'), keywords: t('command.enrollmentKeywords'), run: () => changeView('enrollment') },
      { id: 'view:rollouts', label: t('command.goRollouts'), keywords: t('command.rolloutsKeywords'), run: () => changeView('rollouts') },
      { id: 'view:providers', label: t('command.goProviders'), keywords: t('command.providersKeywords'), run: () => changeView('providers') },
      { id: 'action:new-rollout', label: t('command.newRollout'), keywords: t('command.newRolloutKeywords'), run: () => changeView('rollouts') },
    ]
    for (const node of nodes) {
      const labels = Object.entries(node.labels ?? {}).map(([key, value]) => `${key}=${value}`).join(' ')
      const keywords = `${node.nodeId} ${node.hostname ?? ''} ${labels}`
      items.push({ id: `open:${node.nodeId}`, label: t('command.openNode', { nodeId: node.nodeId }), hint: node.hostname, keywords, run: () => openNode(node.nodeId) })
      items.push({ id: `probe:${node.nodeId}`, label: t('command.probeNode', { nodeId: node.nodeId }), hint: node.hostname, keywords: t('command.probeNodeKeywords', { keywords }), run: () => void createDeepProbe(node.nodeId) })
    }
    return items
  }, [changeView, createDeepProbe, nodes, openNode, t])

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
      if (key === '5' || key === 'p') {
        event.preventDefault()
        changeView('providers')
        return
      }
      if (key === 'r') {
        event.preventDefault()
        if (view === 'activity') {
          void loadAuditEvents()
        } else if (view === 'rollouts') {
          void loadRollouts()
        } else if (view === 'providers') {
          void loadProviders()
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
  }, [changeView, loadAuditEvents, loadProviders, loadRollouts, refreshFleet, view])

  return (
    <div data-sideplane-theme={theme} className="min-h-screen bg-[var(--sp-bg)] text-[var(--sp-text)]">
      <div className="flex min-h-screen flex-col md:h-screen md:flex-row md:overflow-hidden">
        <Sidebar
          currentView={view}
          groups={groups}
          liveConnected={liveConnected}
          lang={lang}
          operatorToken={operatorToken}
          theme={theme}
          onLangToggle={toggleLang}
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
            <EmptyState title={t('node.emptyTitle')} body={t('node.emptyBody')} />
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
          {view === 'providers' && (
            <ProvidersView
              error={providersError}
              loading={providersLoading}
              providers={providers}
              saving={savingProvider}
              onDelete={deleteProvider}
              onRefresh={loadProviders}
              onUpsert={upsertProvider}
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
