import { Dialogs, Events } from '@wailsio/runtime'
import * as WorkflowService from '@bindings/github.com/yottaapp/yotta/internal/services/workflow/service.js'
import type {
  BatchUpdateSourceMetadataRequest,
  BatchUpdateSourceMetadataResult,
  CompileView,
  CreateSourceRequest,
  BundleExportResult,
  BundleInfoView,
  DeleteSourcePreview,
  DeleteSourceRequest,
  DeleteSourceResult,
  PatchView,
  PublishRegistryRequest,
  RunTimelineExportResult,
  RunView,
  RegistrySearchPageView,
  RegistryWorkflowReleaseView,
  SourcePage,
  SourceQuery,
  SourceRecoveryView,
  SourceView,
  StartRunView,
  UpdateSourceMetadataRequest,
} from '@bindings/github.com/yottaapp/yotta/internal/services/workflow/models.js'
import type {
  DebugBreakpoint,
  DebugSnapshot,
} from '@bindings/github.com/yottaapp/yotta/internal/workflow/compiler/models.js'
import type {
  Command as WorkflowPatchCommand,
  JSONValue as WorkflowJSONValue,
} from '../../../../contracts/workflow/current/authoring-patch'
import { callRPC, invoke } from '@/lib/invoke'
import type { Draft as ReviewDraft } from '@bindings/github.com/yottaapp/yotta/internal/communityclient/models.js'

export const parameterTransport = {
  saveTargetBindings: (workflowId: string, revision: number, bindings: Record<string, string>) =>
    invoke(WorkflowService.SaveTargetBindings, workflowId, revision, bindings),
  get: (workflowId: string) => invoke(WorkflowService.GetParameters, workflowId),
  save: (workflowId: string, revision: number, values: Record<string, unknown>) =>
    invoke(WorkflowService.SaveParameters, workflowId, revision, values),
}

export const communityTransport = {
  submitReport: (
    draft: import('@bindings/github.com/yottaapp/yotta/internal/communityclient/models.js').ReportDraft,
  ) => invoke(WorkflowService.SubmitWorkflowReport, draft),
  reports: (workflowId: string) => invoke(WorkflowService.MyWorkflowReports, workflowId),
  replies: (id: string, parent: string, cursor: string) =>
    invoke(WorkflowService.WorkflowReviewReplies, id, parent, cursor),
  list: (id: string, cursor = '') => invoke(WorkflowService.WorkflowReviews, id, cursor),
  mine: (id: string) => invoke(WorkflowService.MyWorkflowReview, id),
  save: (draft: ReviewDraft) => invoke(WorkflowService.SaveWorkflowReview, draft),
  remove: (id: string) => invoke(WorkflowService.DeleteWorkflowReview, id),
  reply: (id: string, parent: string, content: string) =>
    invoke(WorkflowService.ReplyToWorkflowReview, id, parent, content),
  removeReply: (id: string, reply: string) =>
    invoke(WorkflowService.DeleteWorkflowReply, id, reply),
}
import type { SearchOptions } from '@bindings/github.com/yottaapp/yotta/internal/registryclient/models.js'

export const shopTransport = {
  openWallet: () => invoke(WorkflowService.OpenRegistryWallet),
  wallet: () => invoke(WorkflowService.RegistryWallet),
  authorizeWallet: () => invoke(WorkflowService.AuthorizeRegistryWallet),
  cancelWalletAuthorization: () => invoke(WorkflowService.CancelRegistryWalletAuthorization),
  payWallet: (
    input: import('@bindings/github.com/yottaapp/yotta/internal/registryclient/models.js').WalletPayment,
  ) => invoke(WorkflowService.PayRegistryWallet, input),
  renewPaymentSession: (orderNo: string) =>
    invoke(WorkflowService.RenewRegistryPaymentSession, orderNo),
  nativePayment: (orderNo: string, confirm = false) =>
    invoke(WorkflowService.RegistryNativePayment, orderNo, confirm),
  paymentMethods: () => invoke(WorkflowService.RegistryPaymentMethods),
  checkout: (
    workflowId: string,
    input: import('@bindings/github.com/yottaapp/yotta/internal/registryclient/models.js').CheckoutInput,
  ) => invoke(WorkflowService.CreateRegistryCheckout, workflowId, input),
  checkoutState: (orderNo: string, action: 'status' | 'sync' | 'cancel') =>
    invoke(WorkflowService.RegistryCheckoutState, orderNo, action),
  categories: () => invoke(WorkflowService.RegistryCategories),
  filterCatalog: () => invoke(WorkflowService.RegistryFilterCatalog),
  refreshAccount: () => invoke(WorkflowService.RefreshRegistryAccount),
  openAccountCenter: () => invoke(WorkflowService.OpenAccountCenter),
  openMySubmissions: () => invoke(WorkflowService.OpenMySubmissions),
  publicationHistory: (workflowId: string) =>
    invoke(WorkflowService.RegistryWorkflowPublicationHistory, workflowId),
  discover: (query: SearchOptions) => invoke(WorkflowService.DiscoverRegistry, query),
  account: () => invoke(WorkflowService.RegistryAccount),
  login: () => invoke(WorkflowService.LoginRegistry),
  register: () => invoke(WorkflowService.RegisterRegistry),
  cancelLogin: () => invoke(WorkflowService.CancelRegistryLogin),
  logout: () => invoke(WorkflowService.LogoutRegistry),
  installations: () => invoke(WorkflowService.RegistryInstallations),
  clone: (workflowId: string) => invoke(WorkflowService.CloneSource, workflowId),
}

export interface RunChangedEvent {
  runId: string
  workflowId: string
  status: string
  generation: number
  recordDigest: string
  failed?: boolean
}

export interface DebugChangedEvent {
  runId: string
  snapshot: DebugSnapshot
}

export interface RunReadinessView {
  state: string
  code?: string
  graphId?: string
  nodeId?: string
  fromNodeId?: string
  fromPortId?: string
  toNodeId?: string
  toPortId?: string
  requirementId?: string
  slot?: string
}

export type WorkflowStartRunView = StartRunView & { readiness?: RunReadinessView }

export interface WorkflowTransport {
  listSources(): Promise<SourceView[]>
  querySources(query: SourceQuery): Promise<SourcePage>
  listSourceRecoveries(): Promise<SourceRecoveryView[]>
  repairSourceRecovery(recoveryId: string, sourceJson: string): Promise<SourceView>
  deleteSourceRecovery(recoveryId: string): Promise<void>
  previewDeleteSources(workflowIds: string[]): Promise<DeleteSourcePreview[]>
  deleteSources(requests: DeleteSourceRequest[]): Promise<DeleteSourceResult[]>
  createSource(name: string): Promise<SourceView>
  createSourceWithMetadata(request: CreateSourceRequest): Promise<SourceView>
  updateSourceMetadata(
    workflowId: string,
    baseRevision: number,
    request: UpdateSourceMetadataRequest,
  ): Promise<SourceView>
  batchUpdateSourceMetadata(
    requests: BatchUpdateSourceMetadataRequest[],
  ): Promise<BatchUpdateSourceMetadataResult[]>
  chooseSourceBundle(): Promise<string>
  chooseSourceBundleDestination(filename: string): Promise<string>
  chooseSourceBundleDirectory(): Promise<string>
  inspectSourceBundle(path: string): Promise<BundleInfoView>
  previewSourceBundle(workflowId: string): Promise<BundleInfoView>
  importSourceBundle(path: string): Promise<SourceView>
  replaceSourceFromBundle(
    path: string,
    workflowId: string,
    revision: number,
    sourceHash: string,
  ): Promise<SourceView>
  exportSourceBundle(workflowId: string, destination: string): Promise<BundleExportResult>
  exportSourceBundles(workflowIds: string[], directory: string): Promise<BundleExportResult[]>
  getSource(workflowId: string): Promise<SourceView>
  applyPatch(
    workflowId: string,
    baseRevision: number,
    commands: WorkflowPatchCommand[],
  ): Promise<PatchView>
  checkDraft(sourceJson: string): Promise<CompileView>
  startRun(workflowId: string): Promise<WorkflowStartRunView>
  startDebugRun(workflowId: string, breakpoints: DebugBreakpoint[]): Promise<WorkflowStartRunView>
  getDebugSnapshot(runId: string): Promise<DebugSnapshot>
  controlDebugRun(runId: string, action: 'continue' | 'pause' | 'step'): Promise<DebugSnapshot>
  setDebugBreakpoints(runId: string, breakpoints: DebugBreakpoint[]): Promise<DebugSnapshot>
  getActiveSourceRuns(workflowIds: string[]): Promise<Record<string, string[]>>
  cancelRun(runId: string): Promise<RunView>
  cancelAllRuns(): Promise<void>
  getRunTimeline(runId: string): Promise<RunView>
  getRunTimelinePage(runId: string, page: number, pageSize: number): Promise<RunView>
  chooseRunTimelineDestination(filename: string): Promise<string>
  exportRunTimeline(runId: string, destination: string): Promise<RunTimelineExportResult>
  getAuthoringProjection(): Promise<string>
  getNodePackageDependencies?(): Promise<
    import('../../../../contracts/workflow/current/workflow-source').NodePackageDependency[]
  >
  searchRegistry(search: string, limit: number): Promise<RegistrySearchPageView>
  publishSourceToRegistry(request: PublishRegistryRequest): Promise<RegistryWorkflowReleaseView>
  installRegistryWorkflow(releaseId: string): Promise<SourceView>
  registryCommerce?(workflowId: string): Promise<{
    priceCents: number
    currency: string
    available: boolean
    purchaseUrl: string
    entitled: boolean
  }>
}

export function setEditorContext(
  workflowId: string,
  graphId: string,
  dirty: boolean,
): Promise<void> {
  return invoke(WorkflowService.SetEditorContext, workflowId, graphId, dirty)
}

export const workflowTransport: WorkflowTransport = {
  listSources: () => invoke(WorkflowService.ListSources),
  querySources: (query) => invoke(WorkflowService.QuerySources, query),
  listSourceRecoveries: () => invoke(WorkflowService.ListSourceRecoveries),
  repairSourceRecovery: (recoveryId, sourceJson) =>
    invoke(WorkflowService.RepairSourceRecovery, recoveryId, sourceJson),
  deleteSourceRecovery: (recoveryId) => invoke(WorkflowService.DeleteSourceRecovery, recoveryId),
  previewDeleteSources: (workflowIds) => invoke(WorkflowService.PreviewDeleteSources, workflowIds),
  deleteSources: (requests) => invoke(WorkflowService.DeleteSources, requests),
  createSource: (name) => invoke(WorkflowService.CreateSource, name),
  createSourceWithMetadata: (request) => invoke(WorkflowService.CreateSourceWithMetadata, request),
  updateSourceMetadata: (workflowId, baseRevision, request) =>
    invoke(WorkflowService.UpdateSourceMetadata, workflowId, baseRevision, request),
  batchUpdateSourceMetadata: (requests) =>
    invoke(WorkflowService.BatchUpdateSourceMetadata, requests),
  chooseSourceBundle: () =>
    callRPC('workflow.chooseSourceBundle', () =>
      Dialogs.OpenFile({
        Title: 'Import Workflow Source',
        AllowsMultipleSelection: false,
        CanChooseFiles: true,
        CanChooseDirectories: false,
        Filters: [{ DisplayName: 'Yotta Workflow Source', Pattern: '*.yotta-workflow' }],
      }),
    ),
  chooseSourceBundleDestination: (filename) =>
    callRPC('workflow.chooseSourceBundleDestination', () =>
      Dialogs.SaveFile({
        Title: 'Export Workflow Source',
        Filename: filename,
        CanChooseFiles: true,
        CanChooseDirectories: false,
        Filters: [{ DisplayName: 'Yotta Workflow Source', Pattern: '*.yotta-workflow' }],
      }),
    ),
  chooseSourceBundleDirectory: () =>
    callRPC('workflow.chooseSourceBundleDirectory', () =>
      Dialogs.OpenFile({
        Title: 'Export Workflow Sources',
        AllowsMultipleSelection: false,
        CanChooseFiles: false,
        CanChooseDirectories: true,
        CanCreateDirectories: true,
      }),
    ),
  inspectSourceBundle: (path) => invoke(WorkflowService.InspectSourceBundle, path),
  previewSourceBundle: (workflowId) => invoke(WorkflowService.PreviewSourceBundle, workflowId),
  importSourceBundle: (path) => invoke(WorkflowService.ImportSourceBundle, path),
  replaceSourceFromBundle: (path, workflowId, revision, sourceHash) =>
    invoke(WorkflowService.ReplaceSourceFromBundle, path, workflowId, revision, sourceHash),
  exportSourceBundle: (workflowId, destination) =>
    invoke(WorkflowService.ExportSourceBundle, workflowId, destination),
  exportSourceBundles: (workflowIds, directory) =>
    invoke(WorkflowService.ExportSourceBundles, workflowIds, directory),
  getSource: (workflowId) => invoke(WorkflowService.GetSource, workflowId),
  applyPatch: (workflowId, baseRevision, commands) =>
    invoke(
      WorkflowService.ApplyPatch,
      workflowId,
      baseRevision,
      commands as Parameters<typeof WorkflowService.ApplyPatch>[2],
    ),
  checkDraft: (sourceJson) => invoke(WorkflowService.CheckDraft, sourceJson),
  startRun: async (workflowId) =>
    (await invoke(WorkflowService.StartRun, workflowId)) as WorkflowStartRunView,
  startDebugRun: (workflowId, breakpoints) =>
    invoke(WorkflowService.StartDebugRun, workflowId, breakpoints) as Promise<WorkflowStartRunView>,
  getDebugSnapshot: (runId) => invoke(WorkflowService.GetDebugSnapshot, runId),
  controlDebugRun: (runId, action) => invoke(WorkflowService.ControlDebugRun, runId, action),
  setDebugBreakpoints: (runId, breakpoints) =>
    invoke(WorkflowService.SetDebugBreakpoints, runId, breakpoints),
  getActiveSourceRuns: (workflowIds) =>
    invoke(WorkflowService.GetActiveSourceRuns, workflowIds) as Promise<Record<string, string[]>>,
  cancelRun: (runId) => invoke(WorkflowService.CancelRun, runId),
  cancelAllRuns: () => invoke(WorkflowService.CancelAllRuns),
  getRunTimeline: (runId) => invoke(WorkflowService.GetRunTimeline, runId),
  getRunTimelinePage: (runId, page, pageSize) =>
    invoke(WorkflowService.GetRunTimelinePage, runId, page, pageSize),
  chooseRunTimelineDestination: (filename) =>
    callRPC('workflow.chooseRunTimelineDestination', () =>
      Dialogs.SaveFile({
        Title: 'Export Run Timeline',
        Filename: filename,
        CanChooseFiles: true,
        CanChooseDirectories: false,
        Filters: [{ DisplayName: 'Yotta Run Timeline', Pattern: '*.json' }],
      }),
    ),
  exportRunTimeline: (runId, destination) =>
    invoke(WorkflowService.ExportRunTimeline, runId, destination),
  getAuthoringProjection: async () => {
    await import('@/lib/plugins').then((module) => module.loadPluginMessages())
    return invoke(WorkflowService.GetAuthoringProjection)
  },
  getNodePackageDependencies: async () => {
    const packages = await invoke(WorkflowService.GetNodePackageDependencies)
    return packages.flatMap((p) => {
      const [first, ...rest] = p.nodeRefs
      if (!first) return []
      return [
        {
          ...p,
          nodeRefs: [
            first,
            ...rest,
          ] as import('../../../../contracts/workflow/current/workflow-source').NodePackageDependency['nodeRefs'],
        },
      ]
    })
  },
  searchRegistry: (search, limit) => invoke(WorkflowService.SearchRegistry, search, limit),
  publishSourceToRegistry: (request) => invoke(WorkflowService.PublishSourceToRegistry, request),
  installRegistryWorkflow: (releaseId) =>
    invoke(WorkflowService.InstallRegistryWorkflow, releaseId),
  registryCommerce: (workflowId) => invoke(WorkflowService.RegistryCommerce, workflowId),
}

export type { RegistrySearchPageView, RegistryWorkflowReleaseView }

export function onRunChanged(listener: (event: RunChangedEvent) => void): () => void {
  return Events.On('run:changed', (event: { data?: unknown }) => {
    const payload =
      Array.isArray(event.data) && event.data.length === 1 ? event.data[0] : event.data
    if (!isRunChangedEvent(payload)) return
    listener(payload)
  })
}

export function onDebugChanged(listener: (event: DebugChangedEvent) => void): () => void {
  return Events.On('debug:changed', (event: { data?: unknown }) => {
    const payload =
      Array.isArray(event.data) && event.data.length === 1 ? event.data[0] : event.data
    if (!isDebugChangedEvent(payload)) return
    listener(payload)
  })
}

function isRunChangedEvent(value: unknown): value is RunChangedEvent {
  if (typeof value !== 'object' || value === null) return false
  const candidate = value as Record<string, unknown>
  return (
    typeof candidate.runId === 'string' &&
    typeof candidate.workflowId === 'string' &&
    typeof candidate.status === 'string' &&
    typeof candidate.generation === 'number' &&
    typeof candidate.recordDigest === 'string'
  )
}

function isDebugChangedEvent(value: unknown): value is DebugChangedEvent {
  if (typeof value !== 'object' || value === null) return false
  const candidate = value as Record<string, unknown>
  if (
    typeof candidate.runId !== 'string' ||
    typeof candidate.snapshot !== 'object' ||
    candidate.snapshot === null
  )
    return false
  const snapshot = candidate.snapshot as Record<string, unknown>
  return typeof snapshot.status === 'string' && typeof snapshot.generation === 'number'
}

export type {
  BatchUpdateSourceMetadataRequest,
  BatchUpdateSourceMetadataResult,
  BundleExportResult,
  BundleInfoView,
  CompileView,
  CreateSourceRequest,
  DeleteSourcePreview,
  DeleteSourceRequest,
  DeleteSourceResult,
  PatchView,
  RunTimelineExportResult,
  RunView,
  SourcePage,
  SourceQuery,
  SourceRecoveryView,
  SourceView,
  StartRunView,
  UpdateSourceMetadataRequest,
  WorkflowJSONValue,
  WorkflowPatchCommand,
  DebugBreakpoint,
  DebugSnapshot,
}
