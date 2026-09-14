<template>
  <div class="flex h-full min-h-0 flex-col overflow-hidden bg-default">
    <div v-if="session.phase === 'loading'" class="flex flex-1 items-center justify-center px-8">
      <div class="w-full max-w-xl space-y-3" :aria-label="t('workflow.editor.loading')">
        <USkeleton class="h-10 w-2/3 rounded-lg" />
        <USkeleton class="h-72 w-full rounded-lg" />
      </div>
    </div>

    <div
      v-else-if="session.openFailure && !session.source"
      class="flex flex-1 items-center justify-center p-8"
    >
      <div class="max-w-lg rounded-lg border border-error/35 bg-error/10 p-5" role="alert">
        <h1 class="text-sm font-semibold text-error">
          {{ t('workflow.editor.open_failed') }}
        </h1>
        <p class="mt-2 text-xs leading-5 text-muted">{{ session.openFailure }}</p>
        <UButton
          class="mt-4"
          :label="t('workflow.editor.back')"
          color="neutral"
          @click="router.push('/workflows')"
        />
      </div>
    </div>

    <template v-else-if="session.source && session.authoring">
      <WorkflowEditorToolbar
        :name="session.source.workflow.name"
        :revision="session.baseRevision"
        :dirty="session.dirty || metadataDirty || targetBindingsDirty"
        :context="editorToolbarContext"
        @back="router.push('/workflows')"
        @command="handleEditorToolbarCommand"
      >
        <template #breadcrumbs>
          <template v-for="(graphId, index) in session.graphPath" :key="`${index}:${graphId}`">
            <UIcon name="i-tabler-chevron-right" class="size-3 shrink-0 text-dimmed" />
            <UButton
              :data-testid="`workflow-graph-breadcrumb-${graphId}`"
              class="max-w-36"
              color="neutral"
              variant="ghost"
              size="xs"
              :label="graphLabel(graphId)"
              @click="openGraphAt(index)"
            />
          </template>
        </template>
      </WorkflowEditorToolbar>
      <UAlert
        v-if="
          session.source.graphs.some((g) =>
            g.nodes.some((n) =>
              n.nodeRef.nodeTypeId.startsWith('https://schemas.yotta.dev/nodes/panel/'),
            ),
          )
        "
        class="mx-4 my-2"
        color="warning"
        :title="t('panels.legacy_title')"
        :description="t('panels.legacy_hint')"
      />

      <div
        v-if="creationTemplate"
        class="flex flex-wrap items-center gap-2 border-b border-primary/25 bg-primary/5 px-4 py-2 text-xs text-muted"
        role="status"
      >
        <UIcon :name="creationTemplateIcon" class="size-4 text-primary" aria-hidden="true" />
        <span class="min-w-0 flex-1">
          {{ t(`workflow.template.${creationTemplate}.hint`) }}
        </span>
        <RouterLink
          class="font-medium text-primary hover:underline"
          to="/settings?section=automation"
        >
          {{ t('workflow.template.configure_targets') }}
        </RouterLink>
      </div>

      <div
        v-if="session.saveError"
        data-testid="workflow-save-error"
        class="flex items-center gap-2 border-b border-error/35 bg-error/10 px-4 py-2 text-xs text-error"
        role="alert"
      >
        <span class="min-w-0 flex-1">
          {{
            isRevisionConflict
              ? t('workflow.editor.revision_conflict')
              : t('workflow.editor.save_failed', { message: session.saveError })
          }}
        </span>
        <UButton
          v-if="isRevisionConflict"
          size="xs"
          color="error"
          variant="soft"
          :label="t('workflow.editor.reload_latest')"
          @click="reloadWorkflow"
        />
        <UButton
          v-else-if="session.saveErrorTarget"
          size="xs"
          color="error"
          variant="soft"
          :label="t('workflow.editor.locate_save_error')"
          @click="locateSaveError"
        />
        <UButton
          size="xs"
          color="error"
          variant="ghost"
          icon="i-tabler-x"
          :aria-label="t('common.close')"
          @click="session.dismissSaveError()"
        />
      </div>
      <div class="flex min-h-0 flex-1">
        <WorkflowWorkspaceRail
          :active-panel="workspacePanel"
          :open="workspaceSidebarOpen"
          @select="toggleWorkspacePanel"
        />

        <aside
          v-show="workspaceSidebarOpen"
          data-testid="workflow-workspace-sidebar"
          ref="workspaceRoot"
          class="relative flex shrink-0 flex-col border-r border-default bg-default"
          :style="{ width: `${workspaceSidebarWidth}px` }"
        >
          <div
            role="separator"
            tabindex="0"
            :aria-label="t('workflow.sidebar.resize_workspace')"
            aria-orientation="vertical"
            class="absolute inset-y-0 right-0 z-30 w-1 cursor-col-resize transition-colors hover:bg-primary/50 focus-visible:bg-primary/70 focus-visible:outline-none"
            @pointerdown="startSidebarResize('workspace', $event)"
            @keydown.left.prevent="
              workspaceSidebarWidth = resizeWorkspaceSidebar(workspaceSidebarWidth, -16)
            "
            @keydown.right.prevent="
              workspaceSidebarWidth = resizeWorkspaceSidebar(workspaceSidebarWidth, 16)
            "
          />
          <WorkflowGraphManager
            v-if="workspacePanel === 'graphs'"
            :source="session.source"
            :current-graph-id="session.currentGraph?.id"
            :callable-graph-ids="callableGraphIds"
            :drag-format="GRAPH_CALL_DRAG_FORMAT"
            @open="openCalledGraph"
            @insert="addGraphCall"
            @create="openGraphDialog('create')"
            @rename="openGraphDialog('rename', $event)"
            @duplicate="duplicateGraphDefinition"
            @delete="deleteGraphDefinition"
            @delete-cascade="deleteGraphDefinitionCascade"
            @locate="locateGraphCall"
          />
          <WorkflowStatePanel
            v-else-if="workspacePanel === 'variables'"
            :variables="session.source?.variables ?? []"
            :types="session.authoring?.body.types ?? []"
            :references="stateReferenceLocations"
            :type-change-impact="stateTypeChangeImpact"
            @parameters="openWorkflowSettings()"
            @command="applyCommand"
            @insert="insertStateReferenceAtCenter"
            @locate="locateStateReference"
            @locate-reference="locateStateReferenceAt"
            @close="workspaceSidebarOpen = false"
          />
          <WorkflowPathDock
            v-else-if="workspacePanel === 'path'"
            :source="session.source"
            @use="useWorkspaceResource"
          />
          <WorkflowSettingsPanel
            :key="`${session.workflowId}:${metadataResetGeneration}`"
            v-show="workspacePanel === 'settings'"
            :workflow-id="session.workflowId"
            :workflow-metadata="workflowMetadata"
            :workflow-settings-busy="workflowSettingsBusy"
            :workflow-settings-error="workflowSettingsError"
            :workflow-targets="workflowTargets"
            :graphs="session.source.graphs"
            :target-binding-draft="targetBindingDraft"
            :target-bindings-dirty="targetBindingsDirty"
            :target-bindings-busy="targetBindingsBusy"
            :target-bindings-error="targetBindingsError"
            :variables="session.source.variables"
            :blocks="session.source.parameterBlocks"
            :types="session.authoring.body.types"
            @metadata="editorRuns.execute({ kind: 'save' })"
            @metadata-draft="metadataDraft = $event"
            @command="applyCommand"
            @binding="(key, value) => (targetBindingDraft[key] = value)"
            @save-bindings="saveTargetBindings"
          />
          <WorkflowResourceDock
            v-if="workspaceResourcePanel"
            :kind="workspaceResourceKind"
            :source="session.source"
            :recording-phase="recording.state.phase"
            :locate-request="resourceLocateRequest"
            @start-recording="openRecordingStart"
            @capture-template="openTemplateCapture"
            @recapture-workflow-resource="openTemplateRecapture"
            @create-workflow-resource-variant="openTemplateCapture($event, 'append')"
            @remove-workflow-resource-variant="removeWorkflowResourceVariant"
            @open-library="router.push('/assets')"
            @edit="openMacroEditor"
            @edit-workflow-resource="openWorkflowResourceEditor"
            @duplicate-workflow-resource="duplicateWorkflowResource"
            @use="useWorkspaceResource"
            @use-workflow="useWorkflowResource"
            @import-workflow-resource="importWorkflowResource"
            @update-workflow-resources="updateWorkflowResources"
            @remove-workflow-resources="removeWorkflowResources"
          />
          <WorkflowSnippetDock
            v-else-if="workspacePanel === 'snippets'"
            :drag-format="SNIPPET_DRAG_FORMAT"
            @use="useSnippet"
            @edit="editSnippet"
            @delete="deleteSnippet"
          />
        </aside>

        <WorkflowEditorCanvas
          :flow-id="flowApi.id"
          :graph-id="session.currentGraph?.id ?? ''"
          :graph-kind="session.currentGraph?.kind"
          :node-drag-active="nodeDragActive"
          :nodes="flowNodes"
          :edges="flowEdges"
          :is-valid-connection="isValidConnection"
          :node-run-status-by-id="nodeRunStatusById"
          :node-diagnostic-severity-by-id="nodeDiagnosticSeverityById"
          :has-breakpoint="hasBreakpoint"
          :debug-mode-active="debugModeActive"
          :is-debug-current="isDebugCurrent"
          :connected-input-ids="connectedInputIDs"
          :target-slot-for-node="targetSlotForNode"
          :selected-node-count="selectedNodeIds.size"
          :selected-edge-count="selectedEdgeIds.size"
          :snap-guides="snapGuides"
          :layouting="layouting"
          v-model:minimap-open="minimapOpen"
          :canvas-assist="canvasAssist"
          :canvas-assist-favorites="canvasAssistFavorites"
          :canvas-assist-node-options="canvasAssistNodeOptions"
          :has-selected-signal-edges="hasSelectedSignalEdges"
          :ai-panel-open="aiPanelOpen"
          :workflow-default-target-label="workflowDefaultTargetLabel"
          :workflow-default-target-slot="workflowDefaultTargetSlot"
          :workflow-default-panel-slot="
            session.source.targetDefaults?.find((d) => d.target === 'panel')?.slot ?? ''
          "
          :workflow-automation-target-items="workflowAutomationTargetItems"
          :has-single-source-edge="Boolean(selectedSourceEdge())"
          :has-active-run="Boolean(session.activeRun)"
          :run-active="runActive"
          :connection-hint="connectionHint"
          :connection-menu="connectionMenu"
          :compatible-connection-candidates="compatibleConnectionCandidates"
          :all-connection-candidates="allConnectionCandidates"
          :connection-error="connectionError"
          :current-graph-element-count="currentGraphElementCount"
          @element="canvasElement = $event"
          @flow-init="setFlowApi"
          @capture-marquee="captureMarqueeSelection"
          @pointer-inside="canvasPointerInside = $event"
          @track-pointer="trackCanvasPointer"
          @wheel="handleCanvasWheel"
          @dragover="continueNodeDrag"
          @dragleave="finishNodeDrag"
          @drop="dropNode"
          @connect="connect"
          @connect-start="startConnection"
          @connect-end="endConnection"
          @node-click="selectNode"
          @edge-click="selectEdge"
          @pane-click="handlePaneClick"
          @selection-end="finishMarqueeSelection"
          @nodes-change="handleNodesChange"
          @node-drag="trackNodeDrag"
          @node-drag-stop="moveNode"
          @edge-double-click="disconnect"
          @command="applyCommand"
          @context-open="selectNodeForContextMenu"
          @copy="copySelection"
          @cut="cutSelection"
          @duplicate="duplicateSelection"
          @collapse="collapseSelection"
          @toggle-disabled="
            (node: Node) =>
              applyCommand({ kind: 'set-node-disabled', nodeId: node.id, disabled: !node.disabled })
          "
          @toggle-breakpoint="
            (nodeId: string) => toggleBreakpoint(session.currentGraph?.id ?? '', nodeId)
          "
          @save-snippet="openSnippetForNode"
          @remove="removeSelection"
          @open-called-graph="openCalledGraph"
          @update-annotation="updateAnnotation"
          @set-edge-reroutes="setEdgeReroutes"
          @align="alignSelection"
          @distribute="distributeSelection"
          @layout="autoLayout"
          @quick-add="openQuickAddFromAssist"
          @add-comment="addComment"
          @add-favorite="addFavoriteNodeFromAssist"
          @insert-node="openInsertNodeFromAssist"
          @make-space="makeSpaceForNode"
          @update-favorites="setCanvasAssistFavorites"
          @update-assist-collapsed="setCanvasAssistCollapsed"
          @toggle-ai="toggleAIReview"
          @update-assist-hidden="setCanvasAssistHidden"
          @set-default-target="setWorkflowDefaultTarget"
          @set-default-panel="session.setTargetDefault('panel', $event)"
          @add-reroute="addEdgeReroute"
          @clear-reroutes="clearEdgeReroutes"
          @clear-run-trace="clearRunTrace"
          @select-connection-candidate="selectConnectionCandidate"
          @close-connection-menu="closeConnectionMenu"
          @add-run-started="addNode(RUN_STARTED_NODE_ID, { x: 120, y: 160 })"
        />
        <aside
          v-if="inspectorSidebarOpen"
          data-testid="workflow-inspector-sidebar"
          ref="inspectorRoot"
          class="relative flex h-full shrink-0 [&>aside]:!w-full"
          :style="{ width: `${inspectorSidebarWidth}px` }"
        >
          <div
            role="separator"
            tabindex="0"
            :aria-label="t('workflow.sidebar.resize_inspector')"
            aria-orientation="vertical"
            class="absolute inset-y-0 left-0 z-30 w-1 cursor-col-resize transition-colors hover:bg-primary/50 focus-visible:bg-primary/70 focus-visible:outline-none"
            @pointerdown="startSidebarResize('inspector', $event)"
            @keydown.left.prevent="
              inspectorSidebarWidth = resizeInspectorSidebar(inspectorSidebarWidth, 16)
            "
            @keydown.right.prevent="
              inspectorSidebarWidth = resizeInspectorSidebar(inspectorSidebarWidth, -16)
            "
          />
          <AIWorkflowReviewPanel
            v-if="aiPanelOpen"
            :workflow-id="session.workflowId"
            :base-revision="session.baseRevision"
            :dirty="session.dirty || metadataDirty || targetBindingsDirty"
            :run-id="session.activeRun?.runId"
            @close="aiPanelOpen = false"
            @accepted="acceptAIProposal"
          />
          <WorkflowGraphCallInspector
            v-else-if="selectedCall && selectedCallGraph"
            :call="selectedCall"
            :graph="selectedCallGraph"
            :ports="selectedCallPorts"
            :resources="session.source?.resources ?? []"
            @update="applyInspectorCommand({ kind: 'update-graph-call', call: $event })"
            @open="openCalledGraph(selectedCallGraph.id)"
            @duplicate="duplicateSelectedGraphCall"
            @fork="forkSelectedGraphCall"
            @expand="expandSelectedGraphCall"
            @remove="applyCommand({ kind: 'remove-graph-call', callId: selectedCall.id })"
            @locate-resource="locateBoundResource"
          />
          <WorkflowGraphInterfacePanel
            v-else-if="session.currentGraph?.kind === 'subgraph' && !selectedNodeId"
            :graph="session.currentGraph"
            :candidates="graphInterfaceCandidates"
            :reference-counts="graphInterfaceReferenceCounts"
            :infer-disabled="!canInferGraphInterface.valid"
            :infer-hint="
              canInferGraphInterface.valid
                ? t('workflow.graphs.infer_interface_hint')
                : canInferGraphInterface.message
            "
            @infer="inferGraphInterface"
            @add="addGraphInterfaceCandidate"
            @rename="renameGraphInterfaceItem"
            @move="moveGraphInterfaceItem"
            @remove="removeGraphInterfaceItem"
          />
          <WorkflowInspector
            v-else
            :node="selectedNode"
            :projection="selectedProjection"
            :variables="session.source?.variables ?? []"
            :target-defaults="session.source?.targetDefaults ?? []"
            :types="session.authoring?.body.types ?? []"
            :connected-input-ids="selectedConnectedInputIDs"
            :resources="session.source?.resources ?? []"
            @command="applyInspectorCommand"
            @capture-template="selectedNode && captureTemplateForNode(selectedNode.id)"
            @locate-resource="locateBoundResource"
          />
        </aside>
      </div>

      <WorkflowRuntimeWorkbench
        v-model:open="runtimeWorkbenchOpen"
        v-model:tab="runtimeWorkbenchTab"
        :run="session.activeRun"
        :snapshot="session.debugSnapshot"
        :debug-busy="debugControlBusy"
        :node-labels="debugNodeLabels"
        :unhandled-routes="unhandledRunRoutes"
        :diagnostics="session.diagnostics"
        :timeline-exporting="timelineExporting"
        @cancel="editorRuns.execute({ kind: 'cancel' })"
        @refresh="editorRuns.execute({ kind: 'refresh' })"
        @export-timeline="editorRuns.execute({ kind: 'export-timeline' })"
        @diagnose-run="openAIDiagnosis"
        @page="(page) => editorRuns.execute({ kind: 'load-timeline-page', page })"
        @focus-node="focusNode"
        @focus="focusDiagnostic"
        @continue="editorRuns.execute({ kind: 'control-debug', action: 'continue' })"
        @pause="editorRuns.execute({ kind: 'control-debug', action: 'pause' })"
        @step="editorRuns.execute({ kind: 'control-debug', action: 'step' })"
      />
    </template>

    <WorkflowEditorDialogs
      v-model:quick-add-open="quickAddOpen"
      :quick-add-intent="quickAddIntent"
      :quick-add-items="quickAddItems"
      :insertable-quick-add-items="insertableQuickAddItems"
      :quick-add-anchor="quickAddAnchor"
      v-model:graph-dialog-open="graphDialogOpen"
      :graph-dialog-mode="graphDialogMode"
      v-model:graph-name="graphName"
      :pending-conversion="pendingConversion"
      :conversion-title="conversionTitle"
      :pending-state-promotion="pendingStatePromotion"
      v-model:state-promotion-name="statePromotionName"
      :state-promotion-error="statePromotionError"
      v-model:node-search-open="nodeSearchOpen"
      v-model:node-search-query="nodeSearchQuery"
      :node-search-results="nodeSearchResults"
      v-model:template-capture-open="templateCaptureOpen"
      :template-capture-intent="templateCaptureIntent"
      v-model:capture-target-slot="captureTargetSlot"
      :recording-target-items="recordingTargetItems"
      :template-capture-busy="templateCaptureBusy"
      v-model:snippet-modal-open="snippetModalOpen"
      :snippet-draft="snippetDraft"
      :snippet-modal-initial="snippetModalInitial"
      :snippets="snippets.items"
      :snippet-save-busy="snippetSaveBusy"
      @choose-quick-add="selectQuickAddItem"
      @commit-graph="commitGraphDialog"
      @cancel-conversion="cancelConversion"
      @apply-conversion="applyConversion"
      @cancel-state-promotion="cancelStatePromotion"
      @commit-state-promotion="commitStatePromotion"
      @select-first-search-result="focusFirstNodeSearchResult"
      @select-search-result="selectNodeSearchResult"
      @capture-template="captureWorkspaceTemplate"
      @save-snippet="saveSnippet"
    />

    <WorkflowRecordingDialogs
      :recording="recordingEditor"
      :targets="recordingTargetItems"
      :categories="macroMetadataCategories"
      :tags="macroMetadataTags"
      v-model:macro-editing="macroEditing"
      v-model:macro-edit-valid="macroEditValid"
      :macro-edit-busy="macroEditBusy"
      v-model:workflow-macro-editing="workflowMacroEditing"
      v-model:workflow-macro-edit-valid="workflowMacroEditValid"
      v-model:workflow-clip-editing="workflowClipEditing"
      v-model:workflow-clip-trim-start-us="workflowClipTrimStartUs"
      v-model:workflow-clip-trim-end-us="workflowClipTrimEndUs"
      :workflow-resource-edit-busy="workflowResourceEditBusy"
      :workflow-clip-preview="workflowClipPreview"
      :workflow-clip-trim-changed="workflowClipTrimChanged"
      @start="editorRecording.execute({ kind: 'start' })"
      @discard="discardPendingRecording"
      @finalize="editorRecording.execute({ kind: 'finalize' })"
      @save-macro="editorResources.execute({ kind: 'save-global-macro' })"
      @save-workflow-macro="editorResources.execute({ kind: 'save-workflow-macro' })"
      @save-workflow-clip="editorResources.execute({ kind: 'save-workflow-clip' })"
    />
  </div>
</template>

<script setup lang="ts">
import {
  POSITION_SOURCE_AUTHORING,
  POSITION_STRING_TYPE,
  connectPositionSource,
} from '@/app/editor/positionSourceAuthoring'
import {
  computed,
  provide,
  defineAsyncComponent,
  nextTick,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import {
  registerMainWindowCloseGuard,
  type MainWindowCloseRequest,
} from '@/app/window/mainWindowCloseGuard'
import { useToast } from '@/composables/useAppToast'
import type { Node as FlowNode, VueFlowStore } from '@vue-flow/core'
import { useWorkflowCanvasStore } from '@/app/editor/useWorkflowCanvasStore'
import '@vue-flow/core/dist/style.css'
import '@vue-flow/core/dist/theme-default.css'
import '@vue-flow/controls/dist/style.css'
import '@vue-flow/minimap/dist/style.css'
import { useI18n } from 'vue-i18n'
import { useLocalStorage } from '@vueuse/core'
import {
  DEFAULT_ANNOTATION_SIZE,
  type EditorCommand,
  type Node,
  type NodeProjection,
} from '@/app/editor/EditorSession'
import type {
  Annotation,
  WorkflowResource,
} from '../../../contracts/workflow/current/workflow-source'
import { createEditorSession } from '@/app/editor/createEditorSession'
import {
  onDebugChanged,
  onRunChanged,
  setEditorContext,
  workflowTransport,
} from '@/app/transport/workflow'
import { useConfirm } from '@/composables/useConfirm'
import { useRecordingStart } from '@/composables/useRecordingStart'
import { useRecordingStartFeedback } from '@/composables/useRecordingStartFeedback'
import WorkflowSettingsPanel from '@/app/editor/WorkflowSettingsPanel.vue'
import { WORKFLOW_TARGETS, targetValueKey, workflowTargetIssue } from '@/app/editor/workflowTargets'
import { parameterTransport } from '@/app/transport/workflow'
import { effectiveTargetSlot } from '@/app/editor/authoringSurface'
import WorkflowEditorToolbar from '@/app/editor/WorkflowEditorToolbar.vue'
import WorkflowWorkspaceRail from '@/app/editor/WorkflowWorkspaceRail.vue'
import { createEditorRunController } from '@/app/editor/EditorRunController'
import {
  useInspectorPersistence,
  commitActiveInspectorInput,
} from '@/app/editor/useInspectorPersistence'
import { createEditorResourceController } from '@/app/editor/EditorResourceController'
import { createEditorRecordingController } from '@/app/editor/EditorRecordingController'
import { createEditorCanvasLayoutController } from '@/app/editor/EditorCanvasLayoutController'
import { createEditorSelectionController } from '@/app/editor/EditorSelectionController'
import {
  createEditorWorkflowMetadataController,
  type WorkflowMetadataDraft,
} from '@/app/editor/EditorWorkflowMetadataController'
import type { EditorToolbarCommand, EditorToolbarContext } from '@/app/editor/editorToolbarModel'
import { useEditorPanelLayout } from '@/app/editor/useEditorPanelLayout'
import {
  useWorkflowNodeSearch,
  type WorkflowNodeSearchResult,
} from '@/app/editor/useWorkflowNodeSearch'
import { useWorkflowConnectionAuthoring } from '@/app/editor/useWorkflowConnectionAuthoring'
import { useWorkflowRuntimeWorkbench } from '@/app/editor/useWorkflowRuntimeWorkbench'
import { useWorkflowSubgraphManagement } from '@/app/editor/useWorkflowSubgraphManagement'
import { useWorkflowSnippetAuthoring } from '@/app/editor/useWorkflowSnippetAuthoring'
import { useWorkflowResourceAuthoring } from '@/app/editor/useWorkflowResourceAuthoring'
import { useWorkflowQuickAdd } from '@/app/editor/useWorkflowQuickAdd'
import { useWorkflowCanvasGestures } from '@/app/editor/useWorkflowCanvasGestures'
import { useWorkflowStateAuthoring } from '@/app/editor/useWorkflowStateAuthoring'
import { useWorkflowCanvasAssist } from '@/app/editor/useWorkflowCanvasAssist'
import {
  useWorkflowEdgeInteractions,
  workflowEdgeId as edgeId,
} from '@/app/editor/useWorkflowEdgeInteractions'
import {
  GRAPH_CALL_DRAG_FORMAT,
  SNIPPET_DRAG_FORMAT,
  useWorkflowEditorDrop,
} from '@/app/editor/useWorkflowEditorDrop'
import type { WorkspaceResourceKind } from '@/app/editor/resourceLocator'
import WorkflowSnippetDock from '@/app/editor/WorkflowSnippetDock.vue'
import type { WorkflowConnectionCandidate } from '@/app/editor/WorkflowConnectionMenu.vue'
import { centerElementAtFlowPosition } from '@/app/editor/editorCanvasCoordinates'
import { useRecordingStore, type RecordingMode } from '@/stores/recording'
import { useSettingsStore } from '@/stores/settings'
import { useAssetsStore } from '@/stores/assets'
import { backend, type AssetSummary } from '@/lib/backend'
import { useSnippetsStore } from '@/stores/snippets'
import { shortcutFromKeyboardEvent } from '@/app/editor/snippetShortcut'
import { resolveEditorKeyboardAction } from '@/app/editor/editorKeyboard'
import { errorMessage } from '@/lib/invoke'
import { awaitWailsEvent } from '@/composables/useWailsEvent'
import { nodeRunStatuses, unhandledExecRouteKeys } from '@/app/editor/runTrace'
import { nodeDiagnosticSeverities, type WorkflowDiagnostic } from '@/app/editor/workflowDiagnostics'
import { compatibleCandidatePorts } from '@/app/editor/connectionCompatibility'
import {
  createWorkflowNodeGestureState,
  projectWorkflowFlowNodes,
  WORKFLOW_NODE_DRAG_SURFACE,
} from '@/app/editor/workflowFlowProjection'
import { analyzeCollapseBoundary, projectGraphBoundaries } from '@/app/editor/workflowGraphBoundary'
import { collapseSelectionErrorReason } from '@/app/editor/collapseSelectionError'
import type { AlignMode, DistributeMode } from '@/app/editor/workflowLayout'

defineOptions({ name: 'WorkflowEditorView' })

const WorkflowEditorDialogs = defineAsyncComponent(
  () => import('@/app/editor/WorkflowEditorDialogs.vue'),
)
const WorkflowGraphManager = defineAsyncComponent(
  () => import('@/app/editor/WorkflowGraphManager.vue'),
)
const WorkflowGraphCallInspector = defineAsyncComponent(
  () => import('@/app/editor/WorkflowGraphCallInspector.vue'),
)
const WorkflowGraphInterfacePanel = defineAsyncComponent(
  () => import('@/app/editor/WorkflowGraphInterfacePanel.vue'),
)
const WorkflowPathDock = defineAsyncComponent(() => import('@/app/editor/WorkflowPathDock.vue'))
const WorkflowResourceDock = defineAsyncComponent(
  () => import('@/app/editor/WorkflowResourceDock.vue'),
)
const WorkflowInspector = defineAsyncComponent(() => import('@/app/editor/WorkflowInspector.vue'))
const WorkflowRuntimeWorkbench = defineAsyncComponent(
  () => import('@/app/editor/WorkflowRuntimeWorkbench.vue'),
)
const WorkflowEditorCanvas = defineAsyncComponent(
  () => import('@/app/editor/WorkflowEditorCanvas.vue'),
)
const WorkflowStatePanel = defineAsyncComponent(() => import('@/app/editor/WorkflowStatePanel.vue'))
const WorkflowRecordingDialogs = defineAsyncComponent(
  () => import('@/app/editor/WorkflowRecordingDialogs.vue'),
)
const AIWorkflowReviewPanel = defineAsyncComponent(
  () => import('@/app/editor/AIWorkflowReviewPanel.vue'),
)

const route = useRoute()
// The shared route can change while this cached instance is still initializing.
const workflowId = String(route.params.id ?? '')
const router = useRouter()
const toast = useToast()
const { confirm, finishPending } = useConfirm()
const { t, te } = useI18n()
const session = createEditorSession(workflowTransport)
const recording = useRecordingStore()
const editorViewActive = ref(true)
const { start: beginRecording } = useRecordingStart()
const { show: showRecordingStartError } = useRecordingStartFeedback()
const settings = useSettingsStore()
const assets = useAssetsStore()
const snippets = useSnippetsStore()
const editorResources = createEditorResourceController({
  port: {
    openWorkflow: (resource) => backend.workflowResources.open(resource),
    rewriteWorkflow: (resource, edit) => backend.workflowResources.rewrite(resource, edit),
    getMacro: (id) => backend.macros.get(id),
    saveMacro: (asset) => backend.macros.save(asset),
  },
  replaceWorkflowResource: (resourceId, resource) =>
    session.apply({ kind: 'replace-resource', resourceId, resource }),
  invalidateAssets: () => assets.invalidate(),
  translate: (key) => t(key),
  showError,
})
const selectedNodeId = ref('')
const workspaceRoot = ref<HTMLElement | null>(null)
const inspectorRoot = ref<HTMLElement | null>(null)
const selectedNodeIds = ref(new Set<string>())
const selectedEdgeIds = ref(new Set<string>())
const selectedEdgeId = computed({
  get: () => [...selectedEdgeIds.value].at(-1) ?? '',
  set: (edgeId: string) => {
    selectedEdgeIds.value = edgeId ? new Set([edgeId]) : new Set()
  },
})
const aiPanelOpen = ref(false)
const statePanelOpen = ref(false)
const inspectorAutoOpen = useLocalStorage('yotta.workflow.inspector.auto-open', true)
const {
  workspacePanel,
  workspaceSidebarOpen,
  workspaceSidebarWidth,
  inspectorSidebarOpen,
  inspectorSidebarWidth,
  toggleWorkspacePanel,
  resizeWorkspaceSidebar,
  resizeInspectorSidebar,
  startSidebarResize,
  stopSidebarResize,
} = useEditorPanelLayout(inspectorAutoOpen.value)
const workspaceResourcePanel = computed(
  () =>
    workspacePanel.value === 'macro' ||
    workspacePanel.value === 'clip' ||
    workspacePanel.value === 'template',
)
const workspaceResourceKind = computed<WorkspaceResourceKind>(() => {
  const panel = workspacePanel.value
  return panel === 'macro' || panel === 'clip' || panel === 'template' ? panel : 'macro'
})
const creationTemplate = computed(() => {
  const value = route.query.template
  return value === 'windows' ||
    value === 'android' ||
    value === 'browser' ||
    value === 'cross-target'
    ? value
    : ''
})
const creationTemplateIcon = computed(() =>
  creationTemplate.value === 'android'
    ? 'i-tabler-brand-android'
    : creationTemplate.value === 'browser'
      ? 'i-tabler-brand-chrome'
      : creationTemplate.value === 'windows'
        ? 'i-tabler-brand-windows'
        : 'i-tabler-devices',
)
const canvasElement = ref<HTMLElement | null>(null)
const minimapOpen = ref(false)
const runtimeWorkbench = useWorkflowRuntimeWorkbench({
  session,
  translate: (key) => t(key),
  showError,
})
const {
  open: runtimeWorkbenchOpen,
  tab: runtimeWorkbenchTab,
  diagnosticsOpen,
  runTimelineOpen,
  debuggerOpen,
  debugModeActive,
  show: openRuntimeWorkbench,
  toggle: toggleRuntimeWorkbench,
  toggleBreakpoint,
  breakpoints: debugBreakpoints,
  hasBreakpoint,
  isCurrent: isDebugCurrent,
} = runtimeWorkbench
const editorRuns = createEditorRunController({
  session,
  commitInputs: () => {
    commitActiveInspectorInput(workspaceRoot.value)
    return inspectorPersistence.commit()
  },
  persistLocalConfiguration: flushEditorConfiguration,
  translate: (key, params) => (params ? t(key, params) : t(key)),
  showError,
  showSuccess,
  openWorkbench: openRuntimeWorkbench,
  focusDebugNode: focusNode,
  activeRun: () => session.activeRun,
  chooseTimelineDestination: (filename) => workflowTransport.chooseRunTimelineDestination(filename),
  exportTimeline: (runId, destination) => workflowTransport.exportRunTimeline(runId, destination),
})
const { saveSucceeded, debugControlBusy, timelineExporting } = editorRuns
const inspectorPersistence = useInspectorPersistence({
  root: () => inspectorRoot.value,
  isDirty: () => session.dirty,
  save: async () => (await editorRuns.execute({ kind: 'save', inputsCommitted: true })).ok,
})
const editorMetadata = createEditorWorkflowMetadataController({
  session,
  port: {
    getSource: async (workflowId) => {
      const source = await workflowTransport.getSource(workflowId)
      return {
        name: source.name,
        description: source.description,
        category: source.category,
        tags: source.tags,
      }
    },
    updateSourceMetadata: (workflowId, baseRevision, draft) =>
      workflowTransport.updateSourceMetadata(workflowId, baseRevision, draft),
  },
  saveCurrent: async () => {
    await session.save()
    return true
  },
  translate: (key) => t(key),
  describeError: errorMessage,
})
const {
  busy: workflowSettingsBusy,
  error: workflowSettingsError,
  metadata: workflowMetadata,
} = editorMetadata
const metadataResetGeneration = ref(0)
const metadataDraft = ref<WorkflowMetadataDraft | null>(null)
const metadataDirty = computed(
  () =>
    metadataDraft.value !== null &&
    JSON.stringify(metadataDraft.value) !== JSON.stringify({ ...workflowMetadata }),
)
async function flushEditorConfiguration(): Promise<boolean> {
  await nextTick()
  if (metadataDirty.value && metadataDraft.value) {
    if (!metadataDraft.value.name.trim()) {
      workflowSettingsError.value = t('workflow.settings_panel.name_required')
      openWorkflowSettings()
      return false
    }
    if (!(await editorMetadata.save({ ...metadataDraft.value }))) {
      openWorkflowSettings()
      return false
    }
    metadataDraft.value = { ...workflowMetadata, tags: [...workflowMetadata.tags] }
  }
  return flushTargetBindings()
}
const workflowTargets = computed(() => session.source?.targets ?? [])
const targetBindingDraft = ref<Record<string, unknown>>({})
const targetBindingConfirmed = ref<Record<string, unknown>>({})
const targetBindingsBusy = ref(false)
const targetBindingsError = ref('')
const targetBindingsDirty = computed(
  () => JSON.stringify(targetBindingDraft.value) !== JSON.stringify(targetBindingConfirmed.value),
)
let targetBindingGeneration = 0
function resolveLocalTarget(id: string): string {
  return workflowTargets.value.some((target) => target.id === id)
    ? String(targetBindingConfirmed.value[targetValueKey(id)] ?? '')
    : id
}
function openWorkflowSettings() {
  workspacePanel.value = 'settings'
  workspaceSidebarOpen.value = true
}
provide(WORKFLOW_TARGETS, {
  targets: workflowTargets,
  resolve: resolveLocalTarget,
  openSettings: openWorkflowSettings,
})
let metadataWorkflowID = ''
watch(
  () => [workspacePanel.value, session.workflowId, Boolean(session.source)] as const,
  ([panel, id, ready]) => {
    if (panel === 'settings' && id && ready && metadataWorkflowID !== id) {
      metadataWorkflowID = id
      void editorMetadata.open()
    }
  },
)
watch(
  () => session.workflowId,
  async (id) => {
    const current = ++targetBindingGeneration
    targetBindingDraft.value = {}
    targetBindingConfirmed.value = {}
    targetBindingsError.value = ''
    if (!id) return
    targetBindingsBusy.value = true
    try {
      const configuration = await parameterTransport.get(id)
      if (current !== targetBindingGeneration) return
      const values = Object.fromEntries(
        Object.entries(configuration.values ?? {}).filter(([key]) => key.startsWith('@target/')),
      )
      targetBindingDraft.value = { ...values }
      targetBindingConfirmed.value = { ...values }
    } catch (cause) {
      if (current === targetBindingGeneration) targetBindingsError.value = errorMessage(cause)
    } finally {
      if (current === targetBindingGeneration) targetBindingsBusy.value = false
    }
  },
  { immediate: true },
)
async function saveTargetBindings() {
  await editorRuns.execute({ kind: 'save' })
}
async function flushTargetBindings(): Promise<boolean> {
  if (!targetBindingsDirty.value) return true
  if (targetBindingsBusy.value) return false
  const id = session.workflowId,
    generation = targetBindingGeneration
  targetBindingsBusy.value = true
  targetBindingsError.value = ''
  try {
    await session.save()
    if (generation !== targetBindingGeneration) return false
    const bindings = Object.fromEntries(
      workflowTargets.value.map((target) => [
        target.id,
        String(targetBindingDraft.value[targetValueKey(target.id)] ?? ''),
      ]),
    )
    const diagnostics = await parameterTransport.saveTargetBindings(
      id,
      session.baseRevision,
      bindings,
    )
    if (generation !== targetBindingGeneration) return false
    const problem = diagnostics?.find((diagnostic) => diagnostic.severity === 'error')
    if (problem) {
      targetBindingsError.value =
        workflowTargetIssue(problem.code, String(problem.params?.parameterLabel ?? ''), t) ??
        t('workflow.parameters.invalid', {
          name: String(problem.params?.parameterLabel ?? ''),
        })
      openWorkflowSettings()
      return false
    }
    targetBindingConfirmed.value = Object.fromEntries(
      Object.entries(bindings).map(([key, value]) => [targetValueKey(key), value]),
    )
    targetBindingDraft.value = { ...targetBindingConfirmed.value }
    return true
  } catch (cause) {
    openWorkflowSettings()
    if (generation === targetBindingGeneration) targetBindingsError.value = errorMessage(cause)
    return false
  } finally {
    if (generation === targetBindingGeneration) targetBindingsBusy.value = false
  }
}
const editorToolbarContext = computed<Omit<EditorToolbarContext, 'dirty'>>(() => ({
  canUndo: session.canUndo,
  canRedo: session.canRedo,
  aiPanelOpen: aiPanelOpen.value,
  statePanelOpen: statePanelOpen.value,
  inspectorOpen: inspectorSidebarOpen.value,
  runActive: runActive.value,
  saving: session.phase === 'saving',
  saveSucceeded: saveSucceeded.value,
  diagnosticCount: session.diagnostics.length,
  diagnosticsOpen: diagnosticsOpen.value,
  hasRunTimeline: Boolean(session.activeRun),
  runTimelineOpen: runTimelineOpen.value,
  debugModeActive: debugModeActive.value,
  debuggerOpen: debuggerOpen.value,
  recordingPhase: recording.state.phase,
}))
const isRevisionConflict = computed(() => session.saveErrorKind === 'revision')
const {
  macroEditing,
  macroEditBusy,
  macroEditValid,
  workflowMacroEditing,
  workflowMacroEditValid,
  workflowClipEditing,
  workflowClipTrimStartUs,
  workflowClipTrimEndUs,
  workflowResourceEditBusy,
  workflowClipPreview,
  workflowClipTrimChanged,
} = editorResources
const macroMetadataCategories = computed(() =>
  [
    ...new Set(
      [
        ...recordingEditor.facetCategories,
        ...(session.source?.resources ?? []).map((resource) => resource.category),
      ]
        .map((value) => value?.trim() ?? '')
        .filter(Boolean),
    ),
  ].sort((left, right) => left.localeCompare(right)),
)
const macroMetadataTags = computed(() =>
  [
    ...new Set([
      ...recordingEditor.facetTags,
      ...(session.source?.resources ?? []).flatMap((resource) => resource.tags ?? []),
    ]),
  ]
    .map((value) => value?.trim() ?? '')
    .filter(Boolean)
    .sort((left, right) => left.localeCompare(right)),
)
const flowApi = shallowRef(useWorkflowCanvasStore())
const getSelectedNodes = computed(() => flowApi.value.getSelectedNodes.value)
const addSelectedNodes = (...args: Parameters<VueFlowStore['addSelectedNodes']>) =>
  flowApi.value.addSelectedNodes(...args)
const findNode = (...args: Parameters<VueFlowStore['findNode']>) => flowApi.value.findNode(...args)
const fitView = (...args: Parameters<VueFlowStore['fitView']>) => flowApi.value.fitView(...args)
const getViewport = (...args: Parameters<VueFlowStore['getViewport']>) =>
  flowApi.value.getViewport(...args)
const removeSelectedNodes = (...args: Parameters<VueFlowStore['removeSelectedNodes']>) =>
  flowApi.value.removeSelectedNodes(...args)
const screenToFlowCoordinate = (...args: Parameters<VueFlowStore['screenToFlowCoordinate']>) =>
  flowApi.value.screenToFlowCoordinate(...args)
const setCenter = (...args: Parameters<VueFlowStore['setCenter']>) =>
  flowApi.value.setCenter(...args)
const setViewport = (...args: Parameters<VueFlowStore['setViewport']>) =>
  flowApi.value.setViewport(...args)
const updateNode = (...args: Parameters<VueFlowStore['updateNode']>) =>
  flowApi.value.updateNode(...args)
function setFlowApi(api: VueFlowStore): void {
  flowApi.value = api
}
const snippetAuthoring = useWorkflowSnippetAuthoring({
  session,
  snippets,
  canvasElement,
  screenToFlowCoordinate,
  selectInsertedNodes,
  showTargetSetup: openWorkflowSettings,
  showSnippetPanel: () => {
    workspacePanel.value = 'snippets'
    workspaceSidebarOpen.value = true
  },
  projectionTitle,
  confirm,
  translate: (key, params) => t(key, params ?? {}),
  showError,
})
const {
  modalOpen: snippetModalOpen,
  saveBusy: snippetSaveBusy,
  draft: snippetDraft,
  modalInitial: snippetModalInitial,
  openForNode: openSnippetForNode,
  edit: editSnippet,
  save: saveSnippet,
  remove: deleteSnippet,
  use: useSnippet,
} = snippetAuthoring
const connectionAuthoring = useWorkflowConnectionAuthoring({
  session,
  canvasElement,
  selectedNodeId,
  selectedNodeIds,
  applyCommand,
  addNode,
  screenToFlowCoordinate,
  uniqueStateName: (base) => uniqueStateName(base),
  translate: (key, params) => t(key, params ?? {}),
  translationExists: (key) => te(key),
  showError,
})
const {
  connectionMenu,
  pendingConversion,
  pendingStatePromotion,
  statePromotionName,
  connectionHint,
  connectionError,
  connect,
  isValidConnection,
  startConnection,
  endConnection,
  closeConnectionMenu,
  selectConnectionCandidate,
  applyConversion,
  cancelConversion,
  conversionTitle,
  conversionNodePosition,
} = connectionAuthoring
const stateAuthoring = useWorkflowStateAuthoring({
  session,
  canvasElement,
  pendingPromotion: pendingStatePromotion,
  promotionName: statePromotionName,
  selectedNodeId,
  selectedNodeIds,
  screenToFlowCoordinate,
  focusNode,
  showVariables: () => {
    workspacePanel.value = 'variables'
    workspaceSidebarOpen.value = true
  },
  translate: (key) => t(key),
  showError,
})
const {
  referenceLocations: stateReferenceLocations,
  promotionError: statePromotionError,
  insertAtCenter: insertStateReferenceAtCenter,
  insert: insertStateReference,
  locate: locateStateReference,
  locateAt: locateStateReferenceAt,
  typeChangeImpact: stateTypeChangeImpact,
  uniqueName: uniqueStateName,
  commitPromotion: commitStatePromotion,
  cancelPromotion: cancelStatePromotion,
} = stateAuthoring
provide(POSITION_SOURCE_AUTHORING, {
  variables: () =>
    (session.source?.variables ?? [])
      .filter(
        (variable) =>
          variable.type.kind === 'ref' && variable.type.ref.typeId === POSITION_STRING_TYPE,
      )
      .map((variable) => ({ label: variable.name, value: variable.name })),
  connect: (nodeId, slot) => connectPositionSource(session, nodeId, slot),
})
const editorDrop = useWorkflowEditorDrop({
  assets,
  screenToFlowCoordinate,
  addNode,
  useSnippet,
  addGraphCall: (graphId, position) => addGraphCall(graphId, position),
  importWorkflowResource: (resource, position) => importWorkflowResource(resource, position),
  usePathResource: (selection, position) => useWorkspaceResource(selection, position),
  useLocalResource: (id, position) => {
    const resource = session.source?.resources.find((item) => item.id === id)
    if (resource) dropLocalResource(resource, position)
  },
  insertStateReference,
  translate: (key) => t(key),
  showError,
})
const {
  active: nodeDragActive,
  continueDrag: continueNodeDrag,
  finishDrag: finishNodeDrag,
  drop: dropNode,
} = editorDrop
const editorSelection = createEditorSelectionController({
  session,
  selectedNodeId,
  selectedNodeIds,
  selectedEdgeIds,
  selectedFlowNodes: () => getSelectedNodes.value,
  findNode,
  addSelectedNodes,
  removeSelectedNodes,
  applyCommand,
  disconnectEdge: (edgeId) => edgeInteractions.disconnect(edgeId),
  clipboard: navigator.clipboard,
  translate: (key) => t(key),
  showError,
})
const editorCanvasLayout = createEditorCanvasLayoutController({
  session,
  canvasElement,
  selectedNodeIds,
  findNode,
  fitView,
  getViewport,
  applyCommand,
  layoutErrorTitle: () => t('workflow.selection.layout_failed'),
  showError,
})
const { snapGuides, layouting, fitCurrentGraph } = editorCanvasLayout
const subgraphManagement = useWorkflowSubgraphManagement({
  session,
  canvasElement,
  selectedNodeId,
  selectedNodeIds,
  screenToFlowCoordinate,
  clearSelection: () => clearEditorSelection(),
  fitCurrentGraph,
  focusNode,
  confirm,
  translate: (key, params) => t(key, params ?? {}),
  warn: (title, description) => toast.add({ title, description, color: 'warning' }),
  showError,
})
const {
  graphDialogOpen,
  graphDialogMode,
  graphName,
  callableGraphIds,
  canInferGraphInterface,
  graphInterfaceCandidates,
  graphInterfaceReferenceCounts,
  selectedCall,
  selectedCallGraph,
  selectedCallPorts,
  graphLabel,
  openCalledGraph,
  openGraphAt,
  openGraphDialog,
  commitGraphDialog,
  inferGraphInterface,
  addGraphInterfaceCandidate,
  renameGraphInterfaceItem,
  moveGraphInterfaceItem,
  removeGraphInterfaceItem,
  addGraphCall,
  duplicateSelectedGraphCall,
  forkSelectedGraphCall,
  expandSelectedGraphCall,
  deleteGraphDefinition,
  duplicateGraphDefinition,
  deleteGraphDefinitionCascade,
  locateGraphCall,
} = subgraphManagement
const nodeGestures = createWorkflowNodeGestureState()
const canvasGestures = useWorkflowCanvasGestures({
  canvasElement,
  selectedNodeId,
  selectedNodeIds,
  selectedEdgeIds,
  inspectorAutoOpen,
  getSelectedNodes: () => getSelectedNodes.value,
  findNode,
  addSelectedNodes,
  removeSelectedNodes,
  getViewport,
  setViewport,
  closeConnectionMenu,
  executeSelectionClear: () => void editorSelection.execute({ kind: 'clear' }),
  showNodeInspector: () => {
    statePanelOpen.value = false
    aiPanelOpen.value = false
    if (inspectorAutoOpen.value) inspectorSidebarOpen.value = true
  },
  dragPositions: (event) => editorCanvasLayout.dragPositions(event),
  trackLivePosition: nodeGestures.track,
  updateLivePosition: (nodeId, position) => updateNode(nodeId, { position }),
  applyPositions: (positions) => editorCanvasLayout.applyPositions(positions),
  clearLivePosition: nodeGestures.clear,
  clearGuides: () => void editorCanvasLayout.execute({ kind: 'clear-guides' }),
})
const {
  pointerInside: canvasPointerInside,
  lastPointer: lastCanvasPointer,
  trackPointer: trackCanvasPointer,
  paneClick: handlePaneClick,
  captureMarquee: captureMarqueeSelection,
  finishMarquee: finishMarqueeSelection,
  wheel: handleCanvasWheel,
  clearSelection: clearEditorSelection,
  selectNode,
  selectNodeForContextMenu,
  nodesChanged: handleNodesChange,
  trackNodeDrag,
  finishNodeDrag: moveNode,
} = canvasGestures
let unsubscribeRun: (() => void) | undefined
let unsubscribeDebug: (() => void) | undefined
let nextPosition = 0
const RUN_STARTED_NODE_ID = 'https://schemas.yotta.dev/nodes/event/run-started'

const catalogNodes = computed(() =>
  (session.authoring?.body.nodes ?? []).filter((projection) => {
    if (session.currentGraph?.kind === 'subgraph' && projection.instruction.kind === 'run-root')
      return false
    return visibleForCreationTemplate(projection)
  }),
)
const hasSelectedSignalEdges = computed(() => {
  const edges = selectedSourceEdges()
  return (
    edges.length > 0 &&
    edges.length === selectedEdgeIds.value.size &&
    edges.every((edge) => edge.channel !== 'data')
  )
})
const quickAdd = useWorkflowQuickAdd({
  session,
  snippets,
  canvasElement,
  catalogNodes,
  selectedEdgeIds,
  selectedNodeId,
  selectedNodeIds,
  lastCanvasPointer,
  selectedSourceEdges: () => selectedSourceEdges(),
  conversionNodePosition,
  screenToFlowCoordinate,
  viewport: getViewport,
  canvasAssistCollapsed: () => settings.data?.ui.canvasAssist?.collapsed ?? false,
  addNode,
  useSnippet,
  projectionTitle,
  categoryLabel,
  catalogSearchText,
  translate: (key) => t(key),
  translationExists: te,
  showError,
})
const {
  open: quickAddOpen,
  anchor: quickAddAnchor,
  intent: quickAddIntent,
  items: quickAddItems,
  insertableItems: insertableQuickAddItems,
  insertionPosition: canvasInsertionPosition,
  show: openQuickAdd,
  showFromAssist: openQuickAddFromAssist,
  showInsertFromAssist: openInsertNodeFromAssist,
  choose: selectQuickAddItem,
} = quickAdd
const canvasAssistController = useWorkflowCanvasAssist({
  session,
  settings,
  catalogNodes,
  canvasElement,
  screenToFlowCoordinate,
  insertionPosition: canvasInsertionPosition,
  projectionTitle,
  addNode,
  makeSpace: () => void editorCanvasLayout.execute({ kind: 'make-space' }),
})
const {
  state: canvasAssist,
  favorites: canvasAssistFavorites,
  nodeOptions: canvasAssistNodeOptions,
  addFavorite: addFavoriteNode,
  addFavoriteFromToolbar: addFavoriteNodeFromAssist,
  makeSpace: makeSpaceForNode,
  setCollapsed: setCanvasAssistCollapsed,
  setHidden: setCanvasAssistHidden,
  setFavorites: setCanvasAssistFavorites,
} = canvasAssistController
const nodeSearch = useWorkflowNodeSearch({
  source: computed(() => session.source),
  nodeProjection: (nodeTypeId) => session.nodeProjection(nodeTypeId),
  projectionTitle,
  focusNode,
})
const nodeSearchOpen = nodeSearch.open
const nodeSearchQuery = nodeSearch.query
const nodeSearchResults = nodeSearch.results

function visibleForCreationTemplate(projection: NodeProjection): boolean {
  const template = creationTemplate.value
  if (!template || template === 'cross-target') return true
  const targetKind =
    template === 'android'
      ? 'android-device'
      : template === 'browser'
        ? 'browser-cdp'
        : 'desktop-window'
  const automationTargets = (projection.configuredTargets ?? []).filter((target) =>
    target.targetKinds.some((kind) =>
      ['desktop-window', 'android-device', 'browser-cdp'].includes(kind),
    ),
  )
  return automationTargets.every((target) => target.targetKinds.includes(targetKind))
}

const flowNodes = computed<FlowNode[]>(() => {
  const graph = session.currentGraph
  if (!graph) return []
  return [
    ...projectWorkflowFlowNodes(
      graph.nodes,
      session.nodeInstanceProjection.bind(session),
      nodeGestures.positions,
    ),
    ...(graph.calls ?? []).flatMap((call) => {
      const callee = session.calleeGraph(call)
      return callee
        ? [
            {
              id: call.id,
              type: 'graph-call',
              position: nodeGestures.positions.get(call.id) ?? call.position,
              data: { call, graph: callee },
              dragHandle: WORKFLOW_NODE_DRAG_SURFACE,
            },
          ]
        : []
    }),
    ...(graph.annotations ?? []).map((annotation) => ({
      id: annotation.id,
      type: 'annotation',
      position: nodeGestures.positions.get(annotation.id) ?? annotation.position,
      data: { annotation },
      dragHandle: '.workflow-node-drag-handle',
    })),
    ...projectGraphBoundaries(graph).nodes,
  ] as FlowNode[]
})

const graphBoundaryProjection = computed(() =>
  session.currentGraph ? projectGraphBoundaries(session.currentGraph) : { nodes: [], edges: [] },
)
const currentGraphElementCount = computed(() => {
  const graph = session.currentGraph
  return graph
    ? graph.nodes.length + (graph.calls?.length ?? 0) + (graph.annotations?.length ?? 0)
    : 0
})
const compatibleConnectionCandidates = computed<WorkflowConnectionCandidate[]>(() => {
  const menu = connectionMenu.value
  if (!menu) return []
  const anchorNode = session.currentGraph?.nodes.find((node) => node.id === menu.anchor.nodeId)
  if (!anchorNode) return []
  const anchorProjection = session.nodeInstanceProjection(anchorNode)
  if (!anchorProjection) return []
  const candidates: WorkflowConnectionCandidate[] = (session.authoring?.body.nodes ?? [])
    .flatMap((projection) =>
      compatibleCandidatePorts(
        anchorProjection,
        menu.anchor.handle,
        projection,
        new Map((session.authoring?.body.types ?? []).map((type) => [type.typeRef.typeId, type])),
      ).map((port) => ({
        key: `${projection.nodeRef.nodeTypeId}:${port.handle.channel}:${port.handle.portId}`,
        nodeTypeId: projection.nodeRef.nodeTypeId,
        title: projectionTitle(projection),
        icon: projection.icon,
        searchText: catalogSearchText(projection),
        handle: port.handle,
        match: port.match,
        conversionKind: projection.conversion?.kind,
      })),
    )
    .sort(
      (left, right) =>
        connectionMatchRank(left.match) - connectionMatchRank(right.match) ||
        left.title.localeCompare(right.title) ||
        left.key.localeCompare(right.key),
    )
  const output =
    menu.anchor.handle.direction === 'output' && menu.anchor.handle.channel === 'data'
      ? anchorProjection.dataOutputs.find((port) => port.id === menu.anchor.handle.portId)
      : undefined
  const outputExpression = output?.type.expression
  const stateType =
    output?.carrier === 'durable' && outputExpression?.kind === 'ref'
      ? session.authoring?.body.types.find(
          (type) =>
            type.typeRef.typeId === outputExpression.ref.typeId &&
            type.typeRef.semanticDigest === outputExpression.ref.semanticDigest &&
            type.traits.includes('durable') &&
            (type.examples.length > 0 || type.control !== 'object'),
        )
      : undefined
  if (stateType) {
    candidates.unshift({
      key: '__promote-output-to-state__',
      nodeTypeId: '__promote-output-to-state__',
      title: t('workflow.state_panel.promote_action'),
      icon: 'database-plus',
      searchText:
        `${t('workflow.state_panel.promote_action')} ${stateType.titleKey && te(stateType.titleKey) ? t(stateType.titleKey) : stateType.typeRef.typeId}`.toLocaleLowerCase(),
      promoteState: true,
      actionHint: t('workflow.state_panel.promote_candidate_hint'),
    })
  }
  return candidates
})

function connectionMatchRank(match: WorkflowConnectionCandidate['match']): number {
  if (match === 'exact') return 0
  if (match === 'generic-bind') return 1
  if (match === 'assignable') return 2
  return 3
}

const allConnectionCandidates = computed<WorkflowConnectionCandidate[]>(() =>
  (session.authoring?.body.nodes ?? [])
    .filter((projection) => projection.instruction.kind !== 'run-root')
    .map((projection) => ({
      key: projection.nodeRef.nodeTypeId,
      nodeTypeId: projection.nodeRef.nodeTypeId,
      title: projectionTitle(projection),
      icon: projection.icon,
      searchText: catalogSearchText(projection),
    }))
    .sort((left, right) => left.title.localeCompare(right.title)),
)

const selectedNode = computed(
  () => session.currentGraph?.nodes.find((node) => node.id === selectedNodeId.value) ?? null,
)
const selectedProjection = computed(() =>
  selectedNode.value ? (session.nodeInstanceProjection(selectedNode.value) ?? null) : null,
)
const runActive = computed(() =>
  session.activeRun
    ? ['QUEUED', 'RUNNING'].includes(session.activeRun.status.toUpperCase())
    : false,
)
const nodeRunStatusById = computed(() =>
  nodeRunStatuses(session.activeRun, session.currentGraph?.id ?? ''),
)
const nodeDiagnosticSeverityById = computed(() =>
  nodeDiagnosticSeverities(session.diagnostics, session.currentGraph?.id ?? ''),
)
const edgeInteractions = useWorkflowEdgeInteractions({
  session,
  graphBoundaryProjection,
  nodeRunStatusById,
  selectedEdgeIds,
  applyCommand,
  selectEdge: (edgeId, additive) =>
    void editorSelection.execute({ kind: 'select-edge', edgeId, additive }),
  translate: (key) => t(key),
  showError,
})
const {
  flowEdges,
  disconnectEvent: disconnect,
  setReroutes: setEdgeReroutes,
  selectedSourceEdge,
  selectedSourceEdges,
  addReroute: addEdgeReroute,
  clearReroutes: clearEdgeReroutes,
  select: selectEdge,
} = edgeInteractions
const debugNodeLabels = computed<Record<string, string>>(() =>
  Object.fromEntries(
    (session.source?.graphs ?? []).flatMap((graph) =>
      graph.nodes.map((node) => {
        const projection = session.nodeProjection(node.nodeRef.nodeTypeId)
        return [node.id, node.label || (projection ? projectionTitle(projection) : node.id)]
      }),
    ),
  ),
)
const unhandledRunRoutes = computed(() =>
  session.source ? [...unhandledExecRouteKeys(session.source)] : [],
)
const recordingTargetItems = computed(() =>
  (settings.data?.automation.targets ?? [])
    .filter((target) => target.targetKind === 'desktop-window')
    .map((target) => ({
      label: `${target.label} · ${target.slot}`,
      value: target.slot,
    })),
)
const editorRecording = createEditorRecordingController({
  port: {
    start: (mode, targetSlot) => beginRecording(mode, targetSlot, 'editor'),
    pause: () => recording.pause(),
    resume: () => recording.resume(),
    stop: () => recording.stop(),
    cancel: () => recording.cancel(),
    discard: (pendingID) => recording.discard(pendingID),
    finalize: (input) => recording.finalize(input),
    claimInvocation: (origin) => recording.claimInvocation(origin),
    queryFacets: async (kind) => {
      const page = await assets.query({
        search: '',
        kind,
        category: '',
        tags: [],
        sort: 'created_desc',
        page: 1,
        pageSize: 1,
        thumbnailBudget: 0,
        recentGUIDs: [],
      })
      return {
        categories: page.categories.map((item) => item.value),
        tags: page.tags.map((item) => item.value),
      }
    },
  },
  snapshot: () => ({
    phase: recording.state.phase,
    pending: recording.state.pending,
    invocation: recording.invocation,
  }),
  targets: () => recordingTargetItems.value,
  selectedTargetSlot: () =>
    resolveLocalTarget(String(selectedNode.value?.config.slot ?? workflowDefaultTargetSlot.value)),
  importResource: (resource) => importWorkflowResource(resource),
  translate: (key) => t(key),
  showError,
  showStartError: (title, error) => showRecordingStartError(title, error),
})
const recordingEditor = editorRecording.state
const workflowDefaultTargetSlot = computed(
  () => session.source?.targetDefaults?.find((item) => item.target === 'target')?.slot ?? '',
)
const workflowAutomationTargetItems = computed(() =>
  workflowTargets.value.map((target) => ({ label: target.name, value: target.id })),
)
const workflowDefaultTargetLabel = computed(
  () =>
    workflowAutomationTargetItems.value.find(
      (target) => target.value === workflowDefaultTargetSlot.value,
    )?.label ?? t('workflow.target_default.automatic'),
)
const resourceAuthoring = useWorkflowResourceAuthoring({
  session,
  assets,
  canvasElement,
  selectedNode,
  selectedNodeId,
  selectedNodeIds,
  defaultTargetSlot: workflowDefaultTargetSlot,
  resolveTargetSlot: resolveLocalTarget,
  recordingTargetSlot: () => recordingEditor.targetSlot,
  recordingTargetItems,
  screenToFlowCoordinate,
  applyCommand,
  selectNodeForContextMenu,
  showResourcePanel: (kind) => {
    workspaceSidebarOpen.value = true
    workspacePanel.value = kind
  },
  openScreenPicker: (mode, id, targetSlot) => backend.tools.openScreenPicker(mode, id, targetSlot),
  waitForPickerResult: (id) =>
    awaitWailsEvent('tools:picker-result', (payload) => payload?.id === id),
  translate: (key) => t(key),
  showError,
})
const {
  locateRequest: resourceLocateRequest,
  captureOpen: templateCaptureOpen,
  captureTargetSlot,
  captureBusy: templateCaptureBusy,
  captureIntent: templateCaptureIntent,
  openCapture: openTemplateCapture,
  openRecapture: openTemplateRecapture,
  captureForNode: captureTemplateForNode,
  captureWorkspaceTemplate,
  removeVariant: removeWorkflowResourceVariant,
  useWorkspaceResource,
  useResource: useWorkflowResource,
  dropResource: dropLocalResource,
  locateBoundResource,
  importResource: importWorkflowResource,
  updateResources: updateWorkflowResources,
  removeResources: removeWorkflowResources,
} = resourceAuthoring

function connectedInputIDs(nodeID: string): ReadonlySet<string> {
  return new Set(
    (session.currentGraph?.edges ?? [])
      .filter((edge) => edge.to.nodeId === nodeID && edge.channel === 'data')
      .map((edge) => edge.to.portId),
  )
}

const selectedConnectedInputIDs = computed<ReadonlySet<string>>(() =>
  selectedNode.value ? connectedInputIDs(selectedNode.value.id) : new Set(),
)

function targetSlotForNode(node: Node, projection: NodeProjection): string {
  return resolveLocalTarget(
    effectiveTargetSlot(projection, node, session.source?.targetDefaults ?? []),
  )
}

function setWorkflowDefaultTarget(value: unknown): void {
  if (typeof value !== 'string' || !workflowTargets.value.some((target) => target.id === value))
    return
  applyCommand({
    kind: 'set-workflow-targets',
    targets: workflowTargets.value.map((target) => ({ ...target, default: target.id === value })),
  })
}

// Serialize updates so delayed RPC completion cannot restore an older editor state.
let editorContextUpdate = Promise.resolve()
watch(
  () =>
    [
      editorViewActive.value,
      session.workflowId,
      session.currentGraph?.id,
      session.dirty || metadataDirty.value || targetBindingsDirty.value,
    ] as const,
  ([active, workflowId, graphId, dirty]) => {
    editorContextUpdate = editorContextUpdate
      .then(() =>
        setEditorContext(active ? workflowId : '', active ? (graphId ?? '') : '', active && dirty),
      )
      .catch((error) => showError(t('workflow.toast.refresh_failed'), error))
  },
  { immediate: true },
)

watch(
  () => recording.state.pending,
  () =>
    void editorRecording.execute({
      kind: 'sync-pending',
      editorActive: editorViewActive.value,
      editorRoute: route.name === 'workflow-edit',
    }),
  { immediate: true },
)

onActivated(() => {
  editorViewActive.value = true
  if (session.source && !session.dirty) {
    void session
      .refreshIfClean()
      .catch((error) => showError(t('workflow.toast.refresh_failed'), error))
  }
  void editorRecording.execute({
    kind: 'sync-pending',
    editorActive: true,
    editorRoute: route.name === 'workflow-edit',
  })
})
onDeactivated(() => {
  editorViewActive.value = false
})

watch(
  () => recording.completionFailure,
  (failure) => {
    if (failure && recording.invocation === 'editor')
      showError(
        t(
          failure.problem.id?.startsWith('recording.start.')
            ? 'workflow.recording.start_failed'
            : 'recordingSave.save_failed',
        ),
        failure.problem,
      )
  },
)

onMounted(async () => {
  document.addEventListener('keydown', handleEditorKeydown)
  await Promise.allSettled([
    settings.loaded ? Promise.resolve() : settings.load(),
    recording.reconcile(),
    snippets.load(),
  ])
  try {
    await session.load(workflowId)
  } catch {
    return
  }
  await focusRequestedNode()
  unsubscribeRun = onRunChanged((event) => {
    if (event.runId === session.activeRun?.runId) void editorRuns.execute({ kind: 'refresh' })
  })
  unsubscribeDebug = onDebugChanged((event) => {
    if (!session.acceptDebugSnapshot(event.runId, event.snapshot)) return
    if (event.snapshot.status === 'paused') openRuntimeWorkbench('debug')
    if (event.snapshot.status === 'paused' && event.snapshot.nodeId) {
      void focusNode(event.snapshot.graphId ? [event.snapshot.graphId] : [], event.snapshot.nodeId)
    }
  })
})

onBeforeUnmount(() => {
  void editorContextUpdate
    .then(() => setEditorContext('', '', false))
    .catch((error) => showError(t('workflow.toast.refresh_failed'), error))
  document.removeEventListener('keydown', handleEditorKeydown)
  unsubscribeRun?.()
  unsubscribeDebug?.()
  editorRuns.dispose()
  connectionAuthoring.dispose()
  stopSidebarResize()
  unregisterMainWindowCloseGuard()
})
onBeforeRouteLeave(async () => (await confirmEditorExit()) !== false)

const unregisterMainWindowCloseGuard = registerMainWindowCloseGuard(confirmEditorExit)

async function confirmEditorExit(
  closeRequest?: MainWindowCloseRequest,
): Promise<boolean | 'handled'> {
  commitActiveInspectorInput(workspaceRoot.value)
  return inspectorPersistence.decideExit((inputsValid) =>
    decideEditorExit(inputsValid, closeRequest),
  )
}

async function decideEditorExit(
  inputsValid: boolean,
  closeRequest?: MainWindowCloseRequest,
): Promise<boolean | 'handled'> {
  if (
    recording.state.phase === 'armed' ||
    recording.state.phase === 'countdown' ||
    recording.state.phase === 'recording' ||
    recording.state.phase === 'paused'
  ) {
    const leaveRecording = await confirm({
      title: t('workflow.recording.leave_title'),
      description: t('workflow.recording.leave_hint'),
      confirmText: t('workflow.recording.leave_action'),
      color: 'warning',
    })
    if (leaveRecording !== true) return false
    closeRequest?.setStage('stopping')
    if (!(await editorRecording.execute({ kind: 'cancel' }))) return false
  }
  if (recordingEditor.pending) {
    const discard = await confirm({
      title: t('recordingSave.discard'),
      description: t('recordingSave.discard_confirm_hint'),
      confirmText: t('common.delete'),
      color: 'error',
    })
    if (discard !== true) return false
    closeRequest?.setStage('stopping')
    if (!(await editorRecording.execute({ kind: 'discard' }))) return false
  }
  if (!session.dirty && !metadataDirty.value && !targetBindingsDirty.value && inputsValid)
    return true
  const decision = await confirm({
    title: t('workflow.editor.leave_title'),
    description: t('workflow.editor.leave_confirm'),
    confirmText: t('workflow.editor.save_and_exit'),
    color: 'primary',
    alternateText: t('workflow.editor.discard_action'),
    alternateValue: 'discard',
    alternateColor: 'error',
    alternatePendingText: closeRequest ? t('workflow.editor.discard_and_exit_pending') : undefined,
  })
  if (decision === true) return (await editorRuns.execute({ kind: 'save' })).ok
  if (decision === 'discard') {
    try {
      closeRequest?.setStage('restoring')
      await nextTick()
      metadataDraft.value = { ...workflowMetadata, tags: [...workflowMetadata.tags] }
      session.discardDraft()
      targetBindingDraft.value = { ...targetBindingConfirmed.value }
      targetBindingsError.value = ''
      if (closeRequest) {
        await closeRequest.close()
        return 'handled'
      }
      return true
    } catch (error) {
      showError(t('workflow.editor.discard_exit_failed'), error)
      return false
    } finally {
      finishPending()
    }
  }
  return decision === 'discard'
}

function openRecordingStart(mode: RecordingMode): void {
  void editorRecording.execute({ kind: 'open-start', mode })
}

async function discardPendingRecording(): Promise<void> {
  if (!recordingEditor.pending) return
  const accepted = await confirm({
    title: t('recordingSave.discard'),
    description: t('recordingSave.discard_confirm_hint'),
    confirmText: t('common.delete'),
    color: 'error',
  })
  if (accepted !== true) return
  await editorRecording.execute({ kind: 'discard' })
}

async function openWorkflowResourceEditor(resource: WorkflowResource): Promise<void> {
  await editorResources.execute({ kind: 'open-workflow', resource })
}

function duplicateWorkflowResource(resource: WorkflowResource): void {
  try {
    session.apply({
      kind: 'add-resource',
      resource: JSON.parse(JSON.stringify(resource)) as WorkflowResource,
    })
  } catch (error) {
    showError(t('workflow.toast.edit_rejected'), error)
  }
}

async function openMacroEditor(asset: AssetSummary): Promise<void> {
  await editorResources.execute({ kind: 'open-global-macro', asset })
}

function applyInspectorCommand(command: EditorCommand): void {
  if (applyCommand(command)) inspectorPersistence.markChanged()
}

function applyCommand(command: EditorCommand): boolean {
  try {
    session.apply(command)
    if (command.kind === 'remove-node' || command.kind === 'remove-nodes') {
      const removed = new Set(command.kind === 'remove-node' ? [command.nodeId] : command.nodeIds)
      selectedNodeIds.value = new Set(
        [...selectedNodeIds.value].filter((nodeId) => !removed.has(nodeId)),
      )
      if (removed.has(selectedNodeId.value)) selectedNodeId.value = ''
    }
    return true
  } catch (error) {
    showError(t('workflow.toast.edit_rejected'), error)
    return false
  }
}

function addNode(nodeTypeId: string, position?: { x: number; y: number }): void {
  if (
    session.currentGraph?.kind === 'subgraph' &&
    session.nodeProjection(nodeTypeId)?.instruction.kind === 'run-root'
  )
    return
  const offset = position ? 0 : nextPosition++ * 28
  applyCommand({
    kind: 'add-node',
    nodeTypeId,
    position: position ?? { x: 100 + offset, y: 100 + offset },
  })
}

function addComment(): void {
  const rect = canvasElement.value?.getBoundingClientRect()
  const viewport = getViewport()
  const zoom = viewport.zoom > 0 ? viewport.zoom : 1
  const center = rect
    ? { x: (rect.width / 2 - viewport.x) / zoom, y: (rect.height * 0.38 - viewport.y) / zoom }
    : { x: 160, y: 160 }
  const position = centerElementAtFlowPosition(center, DEFAULT_ANNOTATION_SIZE)
  const id = session.addAnnotation(position)
  selectedNodeIds.value = new Set([id])
  selectedNodeId.value = id
}

function updateAnnotation(annotation: Annotation): void {
  applyCommand({ kind: 'update-annotation', annotation })
}

function collapseSelection(): void {
  const graph = session.currentGraph
  if (graph) {
    const issue = analyzeCollapseBoundary(graph, selectedNodeIds.value)
    if (issue) {
      const edge =
        issue.kind === 'multiple-entry' ? (issue.edges[1] ?? issue.edges[0]) : issue.edges[0]
      if (edge) selectedEdgeId.value = edgeId(edge)
      const message = t(`workflow.selection.collapse_${issue.kind.replace('-', '_')}`, {
        count: issue.edges.length,
      })
      showError(t('workflow.selection.collapse_rejected'), new Error(message))
      return
    }
  }
  try {
    const callId = session.collapseSelection(
      [...selectedNodeIds.value],
      t('workflow.graphs.default_name'),
    )
    selectedNodeIds.value = new Set([callId])
    selectedNodeId.value = callId
  } catch (error) {
    const reason = collapseSelectionErrorReason(error)
    showError(
      t('workflow.selection.collapse_rejected'),
      new Error(t(`workflow.selection.collapse_${reason}`)),
    )
  }
}

function toggleAIReview(): void {
  aiPanelOpen.value = !aiPanelOpen.value
  if (aiPanelOpen.value) {
    statePanelOpen.value = false
    inspectorSidebarOpen.value = true
  }
}

function openAIDiagnosis(): void {
  aiPanelOpen.value = true
  statePanelOpen.value = false
  inspectorSidebarOpen.value = true
}

function toggleStatePanel(): void {
  toggleWorkspacePanel('variables')
}

function setInspectorVisibility(open: boolean): void {
  inspectorAutoOpen.value = open
  inspectorSidebarOpen.value = open
}

function openNodeSearch(): void {
  nodeSearch.show()
}

function focusFirstNodeSearchResult(): void {
  nodeSearch.selectFirst()
}

async function selectNodeSearchResult(result: WorkflowNodeSearchResult): Promise<void> {
  await nodeSearch.select(result)
}

function handleEditorKeydown(event: KeyboardEvent): void {
  const snippetShortcut = shortcutFromKeyboardEvent(event)
  const snippet =
    canvasPointerInside.value && snippetShortcut
      ? snippets.items.find(
          (item) => item.shortcut?.toLocaleLowerCase() === snippetShortcut.toLocaleLowerCase(),
        )
      : undefined

  const action = resolveEditorKeyboardAction(event, {
    connectionMenuOpen: !!connectionMenu.value,
    canvasPointerInside: canvasPointerInside.value,
    hasNodeSelection: selectedNodeIds.value.size > 0,
    hasSelection: !!(selectedNodeIds.value.size || selectedNodeId.value || selectedEdgeId.value),
    matchedSnippetID: snippet?.id,
    favoriteNodeTypeIds: canvasAssist.value.favoriteNodeTypeIds,
  })
  if (!action) return

  event.preventDefault()
  switch (action.kind) {
    case 'close-connection-menu':
      closeConnectionMenu()
      return
    case 'open-quick-add':
      openQuickAdd()
      return
    case 'use-snippet':
      void useSnippet(action.snippetID, canvasInsertionPosition())
      return
    case 'add-favorite-node':
      addFavoriteNode(action.nodeTypeId)
      return
    case 'clear-selection':
      clearEditorSelection()
      return
    case 'find-node':
      openNodeSearch()
      return
    case 'copy-selection':
      void copySelection()
      return
    case 'cut-selection':
      void cutSelection()
      return
    case 'paste-selection':
      void pasteSelection()
      return
    case 'duplicate-selection':
      duplicateSelection()
      return
    case 'undo':
      session.undo()
      return
    case 'redo':
      session.redo()
      return
    case 'remove-selection':
      removeSelection()
      return
  }
}

function clearRunTrace(): void {
  session.clearRunTrace()
}

function alignSelection(mode: AlignMode): void {
  void editorCanvasLayout.execute({ kind: 'align', mode })
}

function distributeSelection(mode: DistributeMode): void {
  void editorCanvasLayout.execute({ kind: 'distribute', mode })
}

function autoLayout(direction: 'LR' | 'TB'): void {
  void editorCanvasLayout.execute({ kind: 'auto-layout', direction })
}

function removeSelection(): void {
  void editorSelection.execute({ kind: 'remove' })
}

function duplicateSelection(): void {
  void editorSelection.execute({ kind: 'duplicate' })
}

function copySelection(): Promise<void> {
  return editorSelection.execute({ kind: 'copy' })
}

function cutSelection(): Promise<void> {
  return editorSelection.execute({ kind: 'cut' })
}

function pasteSelection(): Promise<void> {
  return editorSelection.execute({ kind: 'paste' })
}

function selectInsertedNodes(nodeIds: string[]): Promise<void> {
  return editorSelection.execute({ kind: 'select-inserted', nodeIds })
}

function handleEditorToolbarCommand(command: EditorToolbarCommand): void {
  switch (command) {
    case 'undo':
      session.undo()
      return
    case 'redo':
      session.redo()
      return
    case 'find-node':
      openNodeSearch()
      return
    case 'toggle-ai':
      toggleAIReview()
      return
    case 'toggle-state':
      toggleStatePanel()
      return
    case 'toggle-inspector':
      setInspectorVisibility(!inspectorAutoOpen.value)
      return
    case 'check-workflow':
      void editorRuns.execute({ kind: 'check-workflow' })
      return
    case 'toggle-diagnostics':
      toggleRuntimeWorkbench('diagnostics')
      return
    case 'toggle-timeline':
      toggleRuntimeWorkbench('timeline')
      return
    case 'toggle-debugger':
      toggleRuntimeWorkbench('debug')
      return
    case 'start-debug':
      void editorRuns.execute({ kind: 'start-debug', breakpoints: debugBreakpoints() })
      return
    case 'pause-recording':
      void editorRecording.execute({ kind: 'pause' })
      return
    case 'resume-recording':
      void editorRecording.execute({ kind: 'resume' })
      return
    case 'stop-recording':
      void editorRecording.execute({ kind: 'stop' })
      return
    case 'run':
      void editorRuns.execute({ kind: 'start' })
      return
    case 'stop':
      void editorRuns.execute({ kind: 'cancel' })
      return
    case 'save':
      void editorRuns.execute({ kind: 'save' })
      return
    case 'settings':
      openWorkflowSettings()
      return
    case 'reload':
      void reloadWorkflow()
  }
}

async function reloadWorkflow(): Promise<void> {
  if (session.dirty || metadataDirty.value || targetBindingsDirty.value) {
    const accepted = await confirm({
      title: t('workflow.editor.discard_title'),
      description: t('workflow.editor.discard_confirm'),
      confirmText: t('workflow.editor.discard_action'),
      color: 'warning',
    })
    if (accepted !== true) return
  }
  try {
    await session.load(session.workflowId)
    targetBindingDraft.value = { ...targetBindingConfirmed.value }
    targetBindingsError.value = ''
    metadataResetGeneration.value++
    metadataDraft.value = null
    metadataWorkflowID = ''
    if (workspacePanel.value === 'settings') {
      metadataWorkflowID = session.workflowId
      await editorMetadata.open()
    }
    selectedNodeId.value = ''
    selectedNodeIds.value = new Set()
    selectedEdgeId.value = ''
  } catch (error) {
    showError(t('workflow.toast.refresh_failed'), error)
  }
}

async function locateSaveError(): Promise<void> {
  const target = session.saveErrorTarget
  if (!target) return
  await focusNode([target.graphId], target.nodeId)
}

async function acceptAIProposal(): Promise<void> {
  selectedNodeId.value = ''
  try {
    await session.load(session.workflowId)
  } catch (error) {
    showError(t('workflow.ai.refresh_failed'), error)
  }
}

async function focusDiagnostic(diagnostic: WorkflowDiagnostic): Promise<void> {
  if (!diagnostic.nodeId) return
  await focusNode(diagnostic.graphPath ?? [], diagnostic.nodeId)
}

async function focusRequestedNode(): Promise<void> {
  const nodeId = typeof route.query.focusNode === 'string' ? route.query.focusNode : ''
  if (!nodeId || !session.source) return
  const requestedPath =
    typeof route.query.focusGraphPath === 'string'
      ? route.query.focusGraphPath.split('/').filter(Boolean)
      : []
  await focusNode(requestedPath.length ? requestedPath : [session.source.entryGraph], nodeId)
}

async function focusNode(graphPath: string[], nodeId: string): Promise<void> {
  try {
    session.openGraphPath(graphPath)
  } catch (error) {
    showError(t('workflow.diagnostics.locate_failed'), error)
    return
  }
  await nextTick()
  removeSelectedNodes(getSelectedNodes.value)
  const node = findNode(nodeId)
  if (!node) return
  addSelectedNodes([node])
  selectedNodeIds.value = new Set([nodeId])
  selectedNodeId.value = nodeId
  statePanelOpen.value = false
  aiPanelOpen.value = false
  if (inspectorAutoOpen.value) inspectorSidebarOpen.value = true
  const width = node.dimensions.width || 230
  const height = node.dimensions.height || 116
  await setCenter(node.position.x + width / 2, node.position.y + height / 2, {
    zoom: 1,
    duration: 180,
  })
}

function projectionTitle(projection: NodeProjection): string {
  if (projection.titleKey && te(projection.titleKey)) return t(projection.titleKey)
  return (
    projection.nodeRef.nodeTypeId.split('/').filter(Boolean).at(-2) ?? projection.nodeRef.nodeTypeId
  )
}

function categoryLabel(category: string): string {
  const key = `workflow.catalog.category.${category}`
  return te(key) ? t(key) : category
}

function catalogSearchText(projection: NodeProjection): string {
  const description =
    projection.descriptionKey && te(projection.descriptionKey) ? t(projection.descriptionKey) : ''
  return [
    projectionTitle(projection),
    description,
    projection.category,
    projection.execution.class,
    projection.nodeRef.nodeTypeId,
    ...projection.tags,
  ]
    .filter(Boolean)
    .join(' ')
    .toLocaleLowerCase()
}

function showError(title: string, error: unknown): void {
  toast.add({
    title,
    description: errorMessage(error),
    color: 'error',
  })
}

function showSuccess(title: string): void {
  toast.add({ title, color: 'success' })
}
</script>

<style scoped src="./WorkflowEditorView.css"></style>
