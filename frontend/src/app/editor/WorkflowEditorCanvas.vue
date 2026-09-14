<template>
  <div
    ref="element"
    data-testid="workflow-canvas"
    :data-graph-id="graphId"
    class="relative min-w-0 flex-1 bg-elevated/15 transition-shadow"
    :class="nodeDragActive ? 'ring-1 ring-inset ring-primary/60' : ''"
    @pointerdown.capture="emit('capture-marquee', $event)"
    @pointerenter="emit('pointer-inside', true)"
    @pointerleave="emit('pointer-inside', false)"
    @pointermove.capture="emit('track-pointer', $event)"
    @wheel.capture="emit('wheel', $event)"
    @dragover="emit('dragover', $event)"
    @dragleave.self="emit('dragleave')"
    @drop="emit('drop', $event)"
  >
    <VueFlow
      :id="flowId"
      :nodes="nodes"
      :edges="edges"
      :delete-key-code="null"
      :selection-key-code="WORKFLOW_CANVAS_INTERACTION.selectionKeyCode"
      :multi-selection-key-code="WORKFLOW_CANVAS_INTERACTION.multiSelectionKeyCode"
      :pan-activation-key-code="WORKFLOW_CANVAS_INTERACTION.panActivationKeyCode"
      :pan-on-drag="WORKFLOW_CANVAS_INTERACTION.panOnDrag"
      :select-nodes-on-drag="WORKFLOW_CANVAS_INTERACTION.selectNodesOnDrag"
      :is-valid-connection="isValidConnection"
      fit-view-on-init
      :min-zoom="0.2"
      :max-zoom="2"
      class="workflow-flow"
      @init="emit('flow-init', $event)"
      @connect="emit('connect', $event)"
      @connect-start="emit('connect-start', $event)"
      @connect-end="emit('connect-end', $event)"
      @node-click="emit('node-click', $event)"
      @edge-click="emit('edge-click', $event)"
      @pane-click="emit('pane-click')"
      @selection-end="emit('selection-end')"
      @nodes-change="emit('nodes-change', $event)"
      @node-drag-start="emit('node-drag', $event)"
      @node-drag="emit('node-drag', $event)"
      @node-drag-stop="emit('node-drag-stop', $event)"
      @edge-double-click="emit('edge-double-click', $event)"
    >
      <template #node-workflow="slotProps">
        <WorkflowNode
          :node="slotProps.data.node"
          :projection="slotProps.data.projection"
          :selected="slotProps.selected"
          :run-status="nodeRunStatusById.get(slotProps.data.node.id)"
          :diagnostic-severity="nodeDiagnosticSeverityById.get(slotProps.data.node.id)"
          :breakpoint="hasBreakpoint(graphId, slotProps.data.node.id)"
          :debug-mode="debugModeActive"
          :debug-current="isDebugCurrent(graphId, slotProps.data.node.id)"
          :connected-input-ids="connectedInputIds(slotProps.data.node.id)"
          :target-slot="targetSlotForNode(slotProps.data.node, slotProps.data.projection)"
          :selection-count="selectedNodeCount"
          @command="emit('command', $event)"
          @context-open="emit('context-open', slotProps.data.node.id)"
          @copy="emit('copy')"
          @cut="emit('cut')"
          @duplicate="emit('duplicate')"
          @collapse="emit('collapse')"
          @toggle-disabled="emit('toggle-disabled', slotProps.data.node)"
          @toggle-breakpoint="emit('toggle-breakpoint', slotProps.data.node.id)"
          @save-snippet="emit('save-snippet', slotProps.data.node)"
          @remove="emit('remove')"
        />
      </template>
      <template #node-graph-call="slotProps">
        <WorkflowGraphCall
          :call="slotProps.data.call"
          :graph="slotProps.data.graph"
          :selected="slotProps.selected"
          @open="emit('open-called-graph', slotProps.data.call.graphId)"
        />
      </template>
      <template #node-graph-boundary="slotProps">
        <WorkflowGraphBoundary :boundary="slotProps.data" />
      </template>
      <template #node-annotation="slotProps">
        <WorkflowAnnotation
          :annotation="slotProps.data.annotation"
          :selected="slotProps.selected"
          @update="emit('update-annotation', $event)"
        />
      </template>
      <template #edge-reroute="slotProps">
        <WorkflowRerouteEdge
          :id="slotProps.id"
          :source-x="slotProps.sourceX"
          :source-y="slotProps.sourceY"
          :target-x="slotProps.targetX"
          :target-y="slotProps.targetY"
          :style="slotProps.style"
          :edge="slotProps.data.edge"
          @update="emit('set-edge-reroutes', slotProps.data.edge, $event)"
        />
      </template>
      <Background :gap="20" :size="1" pattern-color="rgb(113 113 122 / 0.26)" />
      <Controls position="bottom-left" />
      <MiniMap
        v-if="minimapOpen"
        position="bottom-right"
        :pannable="true"
        :zoomable="true"
        node-color="var(--ui-bg-accented)"
        node-stroke-color="var(--ui-border-accented)"
        :node-stroke-width="1"
        mask-color="color-mix(in oklab, var(--ui-bg) 72%, transparent)"
      />
    </VueFlow>
    <div
      v-if="snapGuides.x !== undefined"
      class="pointer-events-none absolute inset-y-0 z-10 w-px bg-primary/70"
      :style="{ left: `${snapGuides.x}px` }"
    />
    <div
      v-if="snapGuides.y !== undefined"
      class="pointer-events-none absolute inset-x-0 z-10 h-px bg-primary/70"
      :style="{ top: `${snapGuides.y}px` }"
    />
    <WorkflowSelectionToolbar
      v-if="selectedNodeCount"
      :count="selectedNodeCount"
      :layouting="layouting"
      @align="emit('align', $event)"
      @distribute="emit('distribute', $event)"
      @auto-layout="emit('layout', $event)"
      @copy="emit('copy')"
      @cut="emit('cut')"
      @duplicate="emit('duplicate')"
      @collapse="emit('collapse')"
      @remove="emit('remove')"
    />
    <WorkflowCanvasAssistToolbar
      v-if="!canvasAssist.hidden"
      :collapsed="canvasAssist.collapsed"
      :favorite-node-type-ids="canvasAssist.favoriteNodeTypeIds"
      :favorites="canvasAssistFavorites"
      :node-options="canvasAssistNodeOptions"
      :selected-node-count="selectedNodeCount"
      :has-selected-edge="hasSelectedSignalEdges"
      :layouting="layouting"
      @add-node="emit('quick-add')"
      @add-comment="emit('add-comment')"
      @add-favorite="emit('add-favorite', $event)"
      @insert-node="emit('insert-node')"
      @make-space="emit('make-space')"
      @layout="emit('layout', $event)"
      @update-favorites="emit('update-favorites', $event)"
      @update-collapsed="emit('update-assist-collapsed', $event)"
    />
    <div
      data-testid="workflow-canvas-context-actions"
      class="absolute right-3 top-3 z-20 flex gap-1 rounded-lg border border-default bg-default/95 p-1 shadow-lg"
    >
      <UButton
        data-testid="workflow-canvas-ai"
        icon="i-tabler-sparkles"
        color="primary"
        :variant="aiPanelOpen ? 'soft' : 'ghost'"
        size="xs"
        :label="t('workflow.ai.open')"
        @click="emit('toggle-ai')"
      />
      <UButton
        data-testid="workflow-canvas-assist-visibility"
        :icon="
          canvasAssist.hidden
            ? 'i-tabler-layout-sidebar-left-expand'
            : 'i-tabler-layout-sidebar-left-collapse'
        "
        color="neutral"
        :variant="canvasAssist.hidden ? 'ghost' : 'soft'"
        size="xs"
        :aria-label="
          t(canvasAssist.hidden ? 'workflow.canvas_assist.show' : 'workflow.canvas_assist.hide')
        "
        :aria-pressed="!canvasAssist.hidden"
        @click="emit('update-assist-hidden', !canvasAssist.hidden)"
      />
      <UButton
        data-testid="workflow-minimap-toggle"
        icon="i-tabler-map-2"
        color="neutral"
        :variant="minimapOpen ? 'soft' : 'ghost'"
        size="xs"
        :aria-label="
          t(minimapOpen ? 'workflow.canvas.hide_minimap' : 'workflow.canvas.show_minimap')
        "
        :aria-pressed="minimapOpen"
        @click="emit('update:minimapOpen', !minimapOpen)"
      />
      <UPopover mode="click" :ui="{ content: 'w-80 p-3' }">
        <UButton
          data-testid="workflow-target-default"
          icon="i-tabler-device-desktop"
          color="neutral"
          variant="ghost"
          size="xs"
          :aria-label="workflowDefaultTargetLabel"
          :title="workflowDefaultTargetLabel"
        />
        <template #content>
          <div class="space-y-2">
            <p class="text-xs font-medium text-highlighted">
              {{ t('workflow.target_default.label') }}
            </p>
            <AdaptiveSelect
              :model-value="workflowDefaultTargetSlot"
              :items="workflowAutomationTargetItems"
              value-key="value"
              label-key="label"
              width-mode="fill"
              :placeholder="t('workflow.target_default.placeholder')"
              @update:model-value="emit('set-default-target', $event)"
            />
          </div>
        </template>
      </UPopover>
      <WorkflowPanelDefault
        :model-value="workflowDefaultPanelSlot ?? ''"
        @update:model-value="emit('set-default-panel', $event)"
      />
      <template v-if="!selectedNodeCount && selectedEdgeCount">
        <template v-if="hasSingleSourceEdge">
          <UButton
            data-testid="workflow-reroute-add"
            icon="i-tabler-point"
            color="neutral"
            variant="ghost"
            size="xs"
            :label="t('workflow.reroute.add')"
            @click="emit('add-reroute')"
          />
          <UButton
            icon="i-tabler-eraser"
            color="neutral"
            variant="ghost"
            size="xs"
            :aria-label="t('workflow.reroute.clear')"
            @click="emit('clear-reroutes')"
          />
        </template>
        <UButton
          icon="i-tabler-trash"
          color="error"
          variant="ghost"
          size="xs"
          :aria-label="t('common.delete')"
          @click="emit('remove')"
        />
      </template>
      <UButton
        v-if="hasActiveRun"
        data-testid="workflow-clear-run-trace"
        icon="i-tabler-route-off"
        color="neutral"
        variant="ghost"
        size="xs"
        :disabled="runActive"
        :aria-label="t('workflow.canvas.clear_run_trace')"
        @click="emit('clear-run-trace')"
      />
      <UPopover mode="click" :ui="{ content: 'w-72 p-3' }">
        <UButton
          data-testid="workflow-canvas-help"
          icon="i-tabler-help-circle"
          color="neutral"
          variant="ghost"
          size="xs"
          :aria-label="t('workflow.canvas.help')"
        />
        <template #content>
          <div class="space-y-2 text-xs">
            <p class="font-medium text-highlighted">{{ t('workflow.canvas.help') }}</p>
            <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-muted">
              <dt class="font-mono text-toned">{{ t('workflow.canvas.marquee_key') }}</dt>
              <dd>{{ t('workflow.canvas.marquee') }}</dd>
              <dt class="font-mono text-toned">Shift</dt>
              <dd>{{ t('workflow.canvas.add_selection') }}</dd>
              <dt class="font-mono text-toned">Ctrl</dt>
              <dd>{{ t('workflow.canvas.toggle_selection') }}</dd>
              <dt class="font-mono text-toned">Space / MMB</dt>
              <dd>{{ t('workflow.canvas.pan') }}</dd>
              <dt class="font-mono text-toned">Delete / Esc</dt>
              <dd>{{ t('workflow.canvas.delete_clear') }}</dd>
            </dl>
          </div>
        </template>
      </UPopover>
    </div>
    <div
      v-if="connectionHint"
      class="pointer-events-none absolute left-1/2 top-3 z-20 -translate-x-1/2 rounded-lg border border-default bg-default/95 px-3 py-1.5 text-[11px] text-muted shadow-lg"
      role="status"
    >
      {{ connectionHint }}
    </div>
    <WorkflowConnectionMenu
      v-if="connectionMenu"
      :position="connectionMenu.canvasPosition"
      :compatible-candidates="compatibleConnectionCandidates"
      :all-candidates="allConnectionCandidates"
      :error="connectionError"
      @select="emit('select-connection-candidate', $event)"
      @close="emit('close-connection-menu')"
    />
    <div
      v-if="graphKind === 'main' && currentGraphElementCount === 0"
      data-testid="workflow-empty-canvas"
      class="pointer-events-none absolute inset-0 z-10 flex items-center justify-center p-8"
    >
      <div
        class="pointer-events-auto max-w-sm rounded-xl border border-default bg-default/95 p-6 text-center shadow-xl"
      >
        <div
          class="mx-auto mb-3 flex size-11 items-center justify-center rounded-xl bg-primary/10 text-primary"
        >
          <UIcon name="i-tabler-player-play" class="size-5" />
        </div>
        <h2 class="text-sm font-semibold text-highlighted">
          {{ t('workflow.empty_canvas.title') }}
        </h2>
        <p class="mt-2 text-xs leading-5 text-muted">
          {{ t('workflow.empty_canvas.description') }}
        </p>
        <UButton
          class="mt-4"
          icon="i-tabler-player-play"
          :label="t('workflow.empty_canvas.add_start')"
          @click="emit('add-run-started')"
        />
      </div>
    </div>
    <div
      v-if="currentGraphElementCount === 0 && graphKind === 'subgraph'"
      data-testid="workflow-subgraph-empty-hint"
      class="pointer-events-none absolute bottom-4 left-1/2 z-10 -translate-x-1/2 rounded-lg border border-default bg-default/90 px-3 py-2 text-center text-[11px] text-muted shadow-sm"
      role="status"
    >
      {{ t('workflow.empty_canvas.subgraph_description') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import WorkflowPanelDefault from './WorkflowPanelDefault.vue'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  VueFlow,
  type Connection,
  type Edge as FlowEdge,
  type EdgeMouseEvent,
  type Node as FlowNode,
  type NodeChange,
  type NodeDragEvent,
  type NodeMouseEvent,
  type OnConnectStartParams,
  type VueFlowStore,
} from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import { MiniMap } from '@vue-flow/minimap'
import type { Edge, EditorCommand, Node, NodeProjection } from './EditorSession'
import type { Annotation } from '../../../../contracts/workflow/current/workflow-source'
import type { NodeRunStatus } from './runTrace'
import type { DiagnosticSeverity } from './workflowDiagnostics'
import type { ConnectionMenuState } from './useWorkflowConnectionAuthoring'
import type { WorkflowConnectionCandidate } from './WorkflowConnectionMenu.vue'
import type { CanvasAssistFavorite } from './WorkflowCanvasAssistToolbar.vue'
import { WORKFLOW_CANVAS_INTERACTION } from './workflowCanvasInteraction'
import type { AlignMode, DistributeMode } from './workflowLayout'
import WorkflowNode from './WorkflowNode.vue'
import WorkflowGraphCall from './WorkflowGraphCall.vue'
import WorkflowGraphBoundary from './WorkflowGraphBoundary.vue'
import WorkflowAnnotation from './WorkflowAnnotation.vue'
import WorkflowRerouteEdge from './WorkflowRerouteEdge.vue'
import WorkflowSelectionToolbar from './WorkflowSelectionToolbar.vue'
import WorkflowCanvasAssistToolbar from './WorkflowCanvasAssistToolbar.vue'
import WorkflowConnectionMenu from './WorkflowConnectionMenu.vue'
import AdaptiveSelect from '@/components/common/AdaptiveSelect.vue'

defineProps<{
  flowId: string
  graphId: string
  graphKind?: string
  nodeDragActive: boolean
  nodes: FlowNode[]
  edges: FlowEdge[]
  isValidConnection: (connection: Connection) => boolean
  nodeRunStatusById: ReadonlyMap<string, NodeRunStatus>
  nodeDiagnosticSeverityById: ReadonlyMap<string, DiagnosticSeverity>
  hasBreakpoint: (graphId: string, nodeId: string) => boolean
  debugModeActive: boolean
  isDebugCurrent: (graphId: string, nodeId: string) => boolean
  connectedInputIds: (nodeId: string) => ReadonlySet<string>
  targetSlotForNode: (node: Node, projection: NodeProjection) => string
  selectedNodeCount: number
  selectedEdgeCount: number
  snapGuides: { x?: number; y?: number }
  layouting: boolean
  minimapOpen: boolean
  canvasAssist: { collapsed: boolean; hidden: boolean; favoriteNodeTypeIds: string[] }
  canvasAssistFavorites: CanvasAssistFavorite[]
  canvasAssistNodeOptions: Array<{ label: string; value: string }>
  hasSelectedSignalEdges: boolean
  aiPanelOpen: boolean
  workflowDefaultTargetLabel: string
  workflowDefaultTargetSlot: string
  workflowDefaultPanelSlot?: string
  workflowAutomationTargetItems: Array<{ label: string; value: string }>
  hasSingleSourceEdge: boolean
  hasActiveRun: boolean
  runActive: boolean
  connectionHint: string
  connectionMenu: ConnectionMenuState | null
  compatibleConnectionCandidates: WorkflowConnectionCandidate[]
  allConnectionCandidates: WorkflowConnectionCandidate[]
  connectionError: string
  currentGraphElementCount: number
}>()

const emit = defineEmits<{
  element: [element: HTMLElement | null]
  'flow-init': [flow: VueFlowStore]
  'capture-marquee': [event: PointerEvent]
  'pointer-inside': [inside: boolean]
  'track-pointer': [event: PointerEvent]
  wheel: [event: WheelEvent]
  dragover: [event: DragEvent]
  dragleave: []
  drop: [event: DragEvent]
  connect: [connection: Connection]
  'connect-start': [params: OnConnectStartParams]
  'connect-end': [event?: MouseEvent | TouchEvent]
  'node-click': [event: NodeMouseEvent]
  'edge-click': [event: EdgeMouseEvent]
  'pane-click': []
  'selection-end': []
  'nodes-change': [changes: NodeChange[]]
  'node-drag': [event: NodeDragEvent]
  'node-drag-stop': [event: NodeDragEvent]
  'edge-double-click': [event: EdgeMouseEvent]
  command: [command: EditorCommand]
  'context-open': [nodeId: string]
  copy: []
  cut: []
  duplicate: []
  collapse: []
  'toggle-disabled': [node: Node]
  'toggle-breakpoint': [nodeId: string]
  'save-snippet': [node: Node]
  remove: []
  'open-called-graph': [graphId: string]
  'update-annotation': [annotation: Annotation]
  'set-edge-reroutes': [edge: Edge, reroutes: Array<{ x: number; y: number }>]
  align: [mode: AlignMode]
  distribute: [mode: DistributeMode]
  layout: [direction: 'LR' | 'TB']
  'quick-add': []
  'add-comment': []
  'add-favorite': [nodeTypeId: string]
  'insert-node': []
  'make-space': []
  'update-favorites': [ids: string[]]
  'update-assist-collapsed': [collapsed: boolean]
  'toggle-ai': []
  'update-assist-hidden': [hidden: boolean]
  'update:minimapOpen': [open: boolean]
  'set-default-target': [value: unknown]
  'set-default-panel': [value: string]
  'add-reroute': []
  'clear-reroutes': []
  'clear-run-trace': []
  'select-connection-candidate': [candidate: WorkflowConnectionCandidate]
  'close-connection-menu': []
  'add-run-started': []
}>()
const element = ref<HTMLElement | null>(null)
onMounted(() => emit('element', element.value))
onBeforeUnmount(() => emit('element', null))
const { t } = useI18n()
</script>

<style scoped src="../../views/WorkflowEditorView.css"></style>
