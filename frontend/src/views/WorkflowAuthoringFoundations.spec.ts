import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const readSource = (path: string) => readFileSync(join(process.cwd(), path), 'utf8')

describe('workflow authoring foundations', () => {
  it('opens a completed recording from the authoritative pending state only once', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const recordingController = readSource('src/app/editor/EditorRecordingController.ts')
    const recordingDialogs = readSource('src/app/editor/WorkflowRecordingDialogs.vue')
    const assets = readSource('src/views/AssetsView.vue')
    const recordingMetadata = readSource('src/components/recording/RecordingMetadataFields.vue')
    expect(editor).not.toContain('if (payload) openRecordingPreview(payload)')
    expect(assets).not.toContain('if (payload) openRecordingSave(payload)')
    expect(editor).toContain('() => recording.state.pending')
    expect(assets).toContain('() => recording.state.pending')
    expect(editor).toContain('createEditorRecordingController({')
    expect(recordingController).toContain('!editorActive')
    expect(recordingController).toContain("snapshot.invocation !== 'editor'")
    expect(editor).toContain('onDeactivated(() =>')
    const recordingModal = recordingDialogs.slice(
      recordingDialogs.indexOf(':open="!!recording.pending"'),
      recordingDialogs.indexOf(':open="!!macroEditing"'),
    )
    expect(recordingModal).toContain('size="3xl"')
    expect(recordingModal).not.toContain('\n      tall')
    expect(recordingModal).not.toContain("t('recordingSave.optional_metadata')")
    expect(recordingModal).toContain('<RecordingMetadataFields')
    expect(editor).toContain('const WorkflowRecordingDialogs = defineAsyncComponent(')
    expect(recordingDialogs).not.toContain('<UInputMenu')
    expect(recordingMetadata).toContain(':create-item="\'always\'"')
    expect(recordingMetadata).toContain('@create="createCategory"')
    expect(recordingMetadata).toContain('@create="createTag"')
    expect(recordingMetadata).toContain('multiple')
    expect(assets).not.toContain("t('recordingSave.optional_metadata')")
  })

  it('keeps node creation contextual without a permanent catalog sidebar', () => {
    const source = readSource('src/views/WorkflowEditorView.vue')
    const assist = readSource('src/app/editor/WorkflowCanvasAssistToolbar.vue')
    const canvas = readSource('src/app/editor/WorkflowEditorCanvas.vue')
    const editorDrop = readSource('src/app/editor/useWorkflowEditorDrop.ts')
    const keyboard = readSource('src/app/editor/editorKeyboard.ts')
    expect(source).not.toContain('v-model="catalogQuery"')
    expect(source).not.toContain('v-for="group in catalogGroups"')
    expect(source).not.toContain('data-testid="workflow-workspace-nodes"')
    expect(canvas).toContain('<WorkflowCanvasAssistToolbar')
    expect(assist).toContain('test-id="workflow-canvas-add-node"')
    expect(assist).toContain('test-id="workflow-annotation-add"')
    expect(assist).toContain('data-testid="workflow-canvas-assist-compact"')
    expect(assist).toContain('@click="emit(\'update-collapsed\', !collapsed)"')
    expect(canvas).toContain('v-if="!canvasAssist.hidden"')
    expect(canvas).toContain('data-testid="workflow-canvas-assist-visibility"')
    expect(canvas).toContain("emit('update-assist-hidden', !canvasAssist.hidden)")
    expect(source).not.toContain('workflow-graph-add-call')
    expect(source).toContain('GRAPH_CALL_DRAG_FORMAT')
    expect(editorDrop).toContain('getData(GRAPH_CALL_DRAG_FORMAT)')
    expect(editorDrop).toContain('options.addGraphCall(graphCallID, position)')
    expect(canvas).toContain("emit('quick-add')")
    expect(keyboard).toContain("event.key === 'Tab'")
    expect(canvas).toContain('data-testid="workflow-empty-canvas"')
    expect(source).toContain('addNode(RUN_STARTED_NODE_ID')
  })

  it('routes ordinary Delete and Backspace through editor commands', () => {
    const source = readSource('src/views/WorkflowEditorView.vue')
    const selection = readSource('src/app/editor/EditorSelectionController.ts')
    const keyboard = readSource('src/app/editor/editorKeyboard.ts')
    const canvas = readSource('src/app/editor/WorkflowEditorCanvas.vue')
    const edges = readSource('src/app/editor/useWorkflowEdgeInteractions.ts')
    expect(canvas).toContain(':delete-key-code="null"')
    expect(keyboard).toContain("event.key === 'Delete' || event.key === 'Backspace'")
    expect(keyboard).toContain(
      'target?.matches(\'input, textarea, select, [contenteditable="true"]\')',
    )
    expect(source).toContain('createEditorSelectionController({')
    expect(selection).toContain("applyCommand({ kind: 'remove-nodes'")
    expect(edges).toContain("applyCommand({ kind: 'disconnect'")
    expect(source).toContain('@edge-click="selectEdge"')
  })

  it('keeps variables in the primary workspace rail and outside the selected-node inspector', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const inspector = readSource('src/app/editor/WorkflowInspector.vue')
    const rail = readSource('src/app/editor/WorkflowWorkspaceRail.vue')
    const toolbarModel = readSource('src/app/editor/editorToolbarModel.ts')
    const generatedField = readSource('src/app/editor/GeneratedFieldEditor.vue')
    expect(editor).toContain('<WorkflowStatePanel')
    expect(editor).toContain("workspacePanel === 'variables'")
    expect(rail).toContain("workspaceItem('variables', 'workflow.state_panel.title'")
    expect(toolbarModel).not.toContain("action('toggle-state'")
    expect(inspector).not.toContain("kind: 'add-state-variable'")
    expect(inspector).toContain('<WorkflowAuthoringSurfaceItem')
    expect(readSource('src/app/editor/WorkflowAuthoringSurfaceItem.vue')).toContain(
      "path: '/settings', query: { section: targetSettingsSection }",
    )
    expect(inspector).toContain('projectionDescription')
    expect(inspector).toContain('@update:model-value="setLabel"')
    expect(readSource('src/app/editor/WorkflowNode.vue')).toContain(
      "t('workflow.node.labeled_title'",
    )
    expect(generatedField).toContain('<USelectMenu')
    expect(generatedField).toContain("t('workflow.inspector.search_target')")
    expect(generatedField).toContain(':virtualize="selectItems.length > 40"')
  })

  it('separates editable macros, precise clips, templates, and recording entry points', () => {
    const source = readSource('src/views/AssetsView.vue')
    const browse = readSource('src/app/asset-library/useAssetLibraryBrowse.ts')
    const router = readSource('src/router/index.ts')
    expect(router).toContain("path: '/assets'")
    expect(source).toContain("openResourceAction(activeTab === 'macros' ? 'macro' : 'precise')")
    expect(browse).toContain("activeTab.value === 'macros'")
    expect(source).toContain('<MacroActionEditor')
    expect(source).toContain("openScreenPicker('template_save'")
    expect(source).toContain('backend.assets.updateMeta')
    expect(source).toContain("'template_recapture'")
    expect(source).toContain('backend.assets.removeVariant')
  })

  it('keeps recording and resource binding inside the workflow workspace', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const dock = readSource('src/app/editor/WorkflowResourceDock.vue')
    const toolbar = readSource('src/app/editor/WorkflowEditorToolbar.vue')
    const resourceController = readSource('src/app/editor/EditorResourceController.ts')
    const resourceAuthoring = readSource('src/app/editor/useWorkflowResourceAuthoring.ts')
    const recordingController = readSource('src/app/editor/EditorRecordingController.ts')
    const recordingDialogs = readSource('src/app/editor/WorkflowRecordingDialogs.vue')
    const editorDrop = readSource('src/app/editor/useWorkflowEditorDrop.ts')

    expect(editor).toContain('<WorkflowResourceDock')
    expect(editor).toContain('const WorkflowResourceDock = defineAsyncComponent(')
    expect(editor).not.toContain(
      "import WorkflowResourceDock from '@/app/editor/WorkflowResourceDock.vue'",
    )
    expect(editor).toContain('@capture-template="openTemplateCapture"')
    expect(resourceAuthoring).toContain("? 'workflow_resource_version' : 'workflow_resource'")
    expect(resourceAuthoring).toContain("'workflow_resource_version'")
    expect(editor).toContain('@recapture-workflow-resource="openTemplateRecapture"')
    expect(editor).toContain(
      '@create-workflow-resource-variant="openTemplateCapture($event, \'append\')"',
    )
    expect(resourceAuthoring).toContain('applyCapturedImageVersion(')
    expect(resourceAuthoring).toContain('intent.variantId')
    expect(dock).toContain("label: t('assets.templates.manage_variants')")
    expect(dock).not.toContain("label: t('workflow.resources.create_version')")
    expect(recordingController).toContain("destination: 'workflow-resource'")
    expect(editorDrop).toContain('snapshotGlobalAssetByID(guid)')
    expect(editor).toContain('@use="useWorkspaceResource"')
    expect(resourceAuthoring).toContain('session.insertLinearDraft(')
    expect(resourceAuthoring).toContain("kind: 'bind-blob'")
    expect(dock).toContain('pageSize = ref(20)')
    expect(dock).toContain('assets.query(')
    expect(dock).toContain("emit('start-recording'")
    expect(dock).toContain("emit('edit', value)")
    expect(dock).toContain(
      "scope = ref<ResourceScope>(props.kind === 'path' ? 'library' : 'workflow')",
    )
    expect(dock).not.toContain('type ResourceMode')
    expect(dock).not.toContain('workflow-resource-mode-')
    expect(dock).toContain(':data-active="scope === candidate.value"')
    expect(dock).toContain('data-testid="workflow-resource-filter-row"')
    expect(dock).toContain('data-testid="workflow-resource-filter-category"')
    expect(dock).toContain('data-testid="workflow-resource-filter-sort"')
    expect(dock).toContain('width-mode="fill"')
    expect(dock).toContain('<UPagination')
    expect(dock).toContain('backend.assets.batchDelete')
    expect(dock).toContain("emit('remove-workflow-resources'")
    expect(dock).toContain('serializeWorkspaceResource(asset.guid)')
    expect(dock).toContain('backend.workflowResources.promote(resource)')
    expect(dock).toContain("t('workflow.resources.promoted'")
    expect(dock).toContain('backend.workflowResources.duplicate(resource)')
    expect(dock).toContain("emit('edit-workflow-resource', value)")
    expect(dock).toContain("t('assets.templates.manage_variants')")
    expect(dock).not.toContain("t('workflow.resources.create_version')")
    expect(dock).toContain("t('workflow.resources.input_clip_summary'")
    expect(editor).toContain('@edit="openMacroEditor"')
    expect(editor).toContain('createEditorResourceController({')
    expect(resourceController).toContain('options.port.getMacro(asset.guid)')
    expect(editor).toContain('@edit-workflow-resource="openWorkflowResourceEditor"')
    expect(resourceController).toContain('options.port.openWorkflow(copy(resource))')
    expect(resourceController).toContain('options.port.rewriteWorkflow(copy(editing.resource)')
    expect(resourceController).toContain('options.replaceWorkflowResource(editing.resource.id')
    expect(recordingDialogs).toContain(':workflow-resource="workflowClipEditing.resource"')
    expect(dock).toContain("allCategoriesValue = '__yotta_all_categories__'")
    expect(dock).not.toContain("value: ''")
    expect(toolbar).not.toContain('workflow-macro-recording-start')
  })

  it('shares a scannable resource list and lets resources be dragged onto the canvas', () => {
    const dock = readSource('src/app/editor/WorkflowResourceDock.vue')
    const assets = readSource('src/views/AssetsView.vue')
    const editorDrop = readSource('src/app/editor/useWorkflowEditorDrop.ts')

    expect(dock).toContain('<AssetLibraryList')
    expect(assets).toContain('<AssetLibraryList')
    expect(dock).toContain('draggable')
    expect(dock).toContain('RESOURCE_DRAG_FORMAT')
    expect(editorDrop).toContain('RESOURCE_DRAG_FORMAT')
    expect(editorDrop).toContain('dropWorkspaceResource')
  })

  it('offers save, discard, and cancel when leaving a dirty workflow', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const dialog = readSource('src/components/common/ConfirmDialog.vue')
    expect(editor).toContain("alternateValue: 'discard'")
    expect(editor).toContain("t('workflow.editor.discard_and_exit_pending')")
    expect(editor).toContain('session.discardDraft()')
    expect(editor).toContain('await closeRequest.close()')
    expect(dialog).toContain(':loading="pending"')
    expect(editor).toContain(
      "if (decision === true) return (await editorRuns.execute({ kind: 'save' })).ok",
    )
    expect(dialog).toContain('data-testid="confirm-alternate"')
  })

  it('keeps durable workflow metadata orchestration outside the view', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const controller = readSource('src/app/editor/EditorWorkflowMetadataController.ts')

    expect(editor).toContain('createEditorWorkflowMetadataController({')
    expect(editor).not.toContain('async function openWorkflowSettings')
    expect(editor).not.toContain('async function saveWorkflowSettings')
    expect(controller).toContain('options.port.getSource(options.session.workflowId)')
    expect(controller).toContain('options.port.updateSourceMetadata(')
    expect(controller).toContain('await options.session.load(workflowId)')
  })

  it('uses a node-generic context menu and keeps visual templates in typed editor fields', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const node = readSource('src/app/editor/WorkflowNode.vue')
    const dock = readSource('src/app/editor/WorkflowResourceDock.vue')
    const inspector = readSource('src/app/editor/WorkflowInspector.vue')
    const surfaceItem = readSource('src/app/editor/WorkflowAuthoringSurfaceItem.vue')

    expect(node).toContain('<UDropdownMenu')
    expect(node).toContain('@contextmenu.prevent.stop="openNodeContextMenu"')
    expect(node).not.toContain('@contextmenu.prevent.stop="emit(\'save-snippet\')"')
    expect(node).toContain('workflow-node-menu-copy')
    expect(node).toContain('workflow-node-menu-toggle-disabled')
    expect(node).toContain('workflow-node-menu-toggle-breakpoint')
    expect(node).toContain('workflow-node-menu-collapse')
    expect(node).toContain('workflow-node-menu-save-snippet')
    expect(node).toContain('workflow-node-menu-remove')
    expect(node).toContain("color: 'error'")
    expect(node).not.toContain('workflow-node-menu-visual-template')
    expect(node).not.toContain('workflow-node-menu-choose-template')
    expect(node).not.toContain('workflow-node-menu-capture-template')
    expect(editor).toContain('@context-open="selectNodeForContextMenu')
    expect(editor).not.toContain('@open-template-resources="openTemplateResources')
    expect(editor).toContain(
      '@capture-template="selectedNode && captureTemplateForNode(selectedNode.id)"',
    )
    expect(inspector).toContain('@capture-template="emit(\'capture-template\')"')
    expect(surfaceItem).toContain('@capture-template="emit(\'capture-template\')"')
    expect(editor).toContain(':kind="workspaceResourceKind"')
    expect(dock).toContain('kind: ResourceKind')
    expect(dock).not.toContain('workflow-resource-tab-')
  })

  it('makes both editor sidebars collapsible and resizable', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    expect(editor).toContain('data-testid="workflow-workspace-sidebar"')
    expect(editor).toContain('data-testid="workflow-inspector-sidebar"')
    expect(editor).toContain('role="separator"')
    expect(editor).toContain('resizeWorkspaceSidebar')
    expect(editor).toContain('resizeInspectorSidebar')
    expect(editor).toContain('toggleWorkspacePanel')
    expect(editor).toContain('inspectorSidebarOpen')
  })

  it('keeps every distinct workspace tool directly available on the rail', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const rail = readSource('src/app/editor/WorkflowWorkspaceRail.vue')
    const graphManager = readSource('src/app/editor/WorkflowGraphManager.vue')

    expect(editor).toContain('<WorkflowWorkspaceRail')
    expect(rail).toContain("workspaceItem('graphs'")
    expect(rail).toContain("workspaceItem('macro'")
    expect(rail).toContain("workspaceItem('clip'")
    expect(rail).toContain("workspaceItem('template'")
    expect(rail).toContain("workspaceItem('snippets'")
    expect(rail).not.toContain('<UDropdownMenu')
    expect(editor).toContain('<WorkflowGraphManager')
    expect(graphManager).toContain('data-testid="workflow-graph-manager"')
    expect(graphManager).not.toContain('workflow-graph-manager-trigger')
    expect(graphManager).not.toContain('<UPopover')
  })

  it('switches from Run State to node properties without overriding a hidden Inspector preference', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const canvasGestures = readSource('src/app/editor/useWorkflowCanvasGestures.ts')
    expect(canvasGestures).toContain('options.showNodeInspector()')
    expect(editor).toContain('statePanelOpen.value = false')
    expect(editor).toContain('if (inspectorAutoOpen.value) inspectorSidebarOpen.value = true')
    expect(editor).toContain('function setInspectorVisibility(')
    expect(editor).toContain('inspectorAutoOpen.value = open')
  })

  it('moves workflow settings to the first sidebar entry while keeping reload in tools', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const toolbar = readSource('src/app/editor/WorkflowEditorToolbar.vue')
    const toolbarModel = readSource('src/app/editor/editorToolbarModel.ts')

    expect(toolbar).toContain('data-testid="workflow-editor-tools"')
    expect(toolbar).toContain('justify-start gap-2 text-left')
    expect(toolbarModel).not.toContain("action('settings')")
    expect(toolbarModel).toContain("action('reload')")
    expect(editor).toContain('<WorkflowSettingsPanel')
    expect(readSource('src/app/editor/WorkflowWorkspaceRail.vue')).toContain(
      "workspaceItem('settings', 'workflow.editor.settings'",
    )
    expect(editor).toContain("case 'settings':")
    expect(editor).toContain("case 'reload':")
  })

  it('keeps one editor command row while locating contextual actions beside their owners', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const toolbar = readSource('src/app/editor/WorkflowEditorToolbar.vue')
    const panel = readSource('src/app/editor/WorkflowGraphInterfacePanel.vue')
    const canvas = readSource('src/app/editor/WorkflowEditorCanvas.vue')

    expect(toolbar).toContain('<slot name="breadcrumbs" />')
    expect(toolbar).toContain('data-testid="workflow-editor-editing"')
    expect(toolbar.indexOf('data-testid="workflow-editor-editing"')).toBeLessThan(
      toolbar.indexOf('data-testid="workflow-editor-actions"'),
    )
    expect(editor).toContain('<template #breadcrumbs>')
    expect(canvas).toContain('data-testid="workflow-canvas-ai"')
    expect(canvas).toContain('data-testid="workflow-target-default"')
    expect(editor).toContain(':callable-graph-ids="callableGraphIds"')
    expect(panel).toContain('data-testid="workflow-graph-infer-interface"')
    expect(panel).toContain(':label="t(\'workflow.graphs.infer_interface\')"')
  })

  it('keeps primary editor commands visible when the window is narrow', () => {
    const toolbar = readSource('src/app/editor/WorkflowEditorToolbar.vue')
    const contextStart = toolbar.indexOf('data-testid="workflow-editor-context"')
    const actionsStart = toolbar.indexOf('data-testid="workflow-editor-actions"')

    expect(toolbar).not.toContain('overflow-x-auto')
    expect(contextStart).toBeGreaterThan(-1)
    expect(actionsStart).toBeGreaterThan(contextStart)
    expect(toolbar.slice(contextStart, actionsStart)).toContain('min-w-0')
    expect(toolbar.slice(contextStart, actionsStart)).toContain('overflow-hidden')
    expect(toolbar.slice(actionsStart, toolbar.indexOf('</header>'))).toContain('shrink-0')
  })

  it('asks for an automation target only when a library creation action starts', () => {
    const source = readSource('src/views/AssetsView.vue')
    const header = source.slice(source.indexOf('<header'), source.indexOf('</header>'))

    expect(header).not.toContain('selectedTargetSlot')
    expect(source).toContain('v-model:open="resourceActionOpen"')
    expect(source).toContain('openResourceAction')
  })

  it('uses one unified macro editor for metadata and recorded actions', () => {
    const source = readSource('src/views/AssetsView.vue')
    const menu = source.slice(
      source.indexOf('function assetMenu'),
      source.indexOf('async function openPreciseWorkbench'),
    )
    const editor = source.slice(
      source.indexOf(':open="!!macroEditing"'),
      source.indexOf(':open="!!variantAsset"'),
    )

    expect(menu).not.toContain("t('assets.macros.edit_actions')")
    expect(menu).toContain("label: t('common.edit')")
    expect(menu).toContain('openMacroEditor')
    expect(editor).toContain('v-model="macroEditing.label"')
    expect(editor).toContain('v-model="macroEditing.category"')
    expect(editor).toContain('v-model="macroEditing.tags"')
    expect(editor).toContain('<MacroActionEditor')
  })

  it('uses the same unified editor from the workflow resource dock', () => {
    const dock = readSource('src/app/editor/WorkflowResourceDock.vue')
    const recordingDialogs = readSource('src/app/editor/WorkflowRecordingDialogs.vue')
    const menu = dock.slice(
      dock.indexOf('function itemMenu'),
      dock.indexOf('async function duplicateWorkflowResource'),
    )
    const globalEditor = recordingDialogs.slice(
      recordingDialogs.indexOf(':open="!!macroEditing"'),
      recordingDialogs.indexOf(':open="!!workflowMacroEditing"'),
    )
    const workflowEditor = recordingDialogs.slice(
      recordingDialogs.indexOf(':open="!!workflowMacroEditing"'),
      recordingDialogs.indexOf(':open="!!workflowClipEditing"'),
    )

    expect(menu).not.toContain("t('assets.macros.edit_actions')")
    expect(menu).not.toContain("value.kind === 'macro' || value.kind === 'input-clip'")
    expect(menu).toContain("value.kind !== 'macro'")
    expect(menu).toContain("emit('edit-workflow-resource', value)")
    expect(menu).toContain("emit('edit', value)")
    expect(globalEditor).toContain('v-model:name="macroEditing.label"')
    expect(globalEditor).toContain('<MacroActionEditor')
    expect(workflowEditor).toContain('v-model:name="workflowMacroEditing.resource.name"')
    expect(workflowEditor).toContain('<MacroActionEditor')
  })

  it('keeps the macro action list full-height and exposes simple plus atomic editing', () => {
    const source = readSource('src/components/recording/MacroActionEditor.vue')

    expect(source).toContain('class="min-h-0 flex-1 overflow-auto"')
    expect(source).not.toContain('max-h-[28rem]')
    expect(source).toContain("viewMode === 'simple'")
    expect(source).toContain("viewMode === 'atomic'")
    expect(source).toContain("menuAction('key-press'")
    expect(source).toContain('duplicateSelected')
    expect(source).toContain('@dragstart.stop="beginDrag(entry.row, $event)"')
    expect(source).not.toContain(':draggable="!search.trim()"')
  })

  it('lets workflow metadata create a category from the editor dialog', () => {
    const dialog = readSource('src/app/editor/WorkflowMetadataDialog.vue')

    expect(dialog).toContain('<UInputMenu')
    expect(dialog).toContain(`:create-item="'always'"`)
    expect(dialog).toContain('@create="createCategory"')
  })

  it('configures one shared global hotkey from workflow list and editor surfaces', () => {
    const list = readSource('src/views/WorkflowsView.vue')
    const dialog = readSource('src/app/editor/WorkflowMetadataDialog.vue')
    const field = readSource('src/components/hotkeys/WorkflowHotkeyField.vue')
    const management = readSource('src/views/SettingsHotkeys.vue')

    expect(list).toContain('<WorkflowHotkeyField')
    expect(dialog).toContain('<WorkflowHotkeyField')
    expect(field).toContain('`action.${props.workflowId}`')
    expect(field).toContain('backend.hotkeys.reconcile()')
    expect(management).toContain("entry.source !== 'action' || entry.status !== 'unbound'")
  })

  it('scales the asset library with server paging, cross-page batches, and guarded cleanup', () => {
    const source = readSource('src/views/AssetsView.vue')
    const browse = readSource('src/app/asset-library/useAssetLibraryBrowse.ts')
    expect(source).toContain('useAssetLibraryBrowse({')
    expect(browse).toContain('options.queryAssets')
    expect(browse).toContain('page: page.value')
    expect(browse).toContain('pageSize: pageSize.value')
    expect(browse).toContain('toggleCurrentPage')
    expect(source).toContain('backend.assets.batchUpdateMeta')
    expect(source).toContain('backend.assets.batchDelete')
    expect(browse).toContain('retainFailedSelection')
    expect(source).toContain('backend.assets.previewCleanup')
    expect(source).toContain('backend.assets.commitCleanup(preview.token)')
  })

  it('uses one paged asset picker boundary for node and graph-call BlobRef bindings', () => {
    const inputEditor = readSource('src/app/editor/WorkflowInputBindingEditor.vue')
    const inspector = readSource('src/app/editor/WorkflowInspector.vue')
    const graphCallInspector = readSource('src/app/editor/WorkflowGraphCallInspector.vue')
    const picker = readSource('src/components/assets/AssetPickerModal.vue')

    expect(inputEditor).toContain('<AssetPickerModal')
    expect(picker).toContain('confirmSelection')
    expect(picker).toContain("'assetPicker.use_template'")
    expect(picker).toContain('assets.query')
    expect(picker).toContain('thumbnailBudget: 12')
    expect(inspector).not.toContain('clipsStore.refresh')
    expect(inspector).not.toContain('templatesStore.reload')
    expect(graphCallInspector).not.toContain('clipsStore.refresh')
  })

  it('uses one expandable Blob preview across the library, picker, and inspector', () => {
    const preview = readSource('src/components/common/BlobPreview.vue')
    const library = readSource('src/components/assets/AssetLibraryList.vue')
    const picker = readSource('src/components/assets/AssetPickerModal.vue')
    const field = readSource('src/app/editor/AssetReferenceField.vue')

    expect(preview).toContain("t('assets.preview_actual_size')")
    expect(preview).toContain('viewerImageStyle')
    expect(library).toContain('expandable')
    expect(picker).toContain('expandable')
    expect(field).toContain('expandable')
  })

  it('locates every shared asset binding through stable workflow or library identity', () => {
    const inputEditor = readSource('src/app/editor/WorkflowInputBindingEditor.vue')
    const inspector = readSource('src/app/editor/WorkflowInspector.vue')
    const graphCallInspector = readSource('src/app/editor/WorkflowGraphCallInspector.vue')
    const dock = readSource('src/app/editor/WorkflowResourceDock.vue')
    const editor = readSource('src/views/WorkflowEditorView.vue')

    expect(inputEditor).toContain('resolveWorkflowResourceBinding')
    expect(inputEditor).toContain("scope: 'workflow'")
    expect(inputEditor).toContain("scope: 'library'")
    expect(inspector).toContain('@locate-resource')
    expect(graphCallInspector).toContain('@locate-resource')
    expect(dock).toContain('applyLocateRequest')
    expect(dock).toContain(':focused-id="focusedResourceId"')
    expect(dock).toContain('#trailing')
    expect(dock).toContain("t('assets.clear_search')")
    expect(dock).not.toContain("t('workflow.resources.located'")
    expect(dock).toContain("t('workflow.resources.locate_failed'")
    expect(dock).toContain('data-testid="workflow-resource-feedback-dismiss"')
    expect(editor).toContain('locateBoundResource')
  })

  it('offers compatible nodes when a typed connection ends on the canvas', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const menu = readSource('src/app/editor/WorkflowConnectionMenu.vue')
    const authoring = readSource('src/app/editor/useWorkflowConnectionAuthoring.ts')
    const canvas = readSource('src/app/editor/WorkflowEditorCanvas.vue')
    const edges = readSource('src/app/editor/useWorkflowEdgeInteractions.ts')
    expect(editor).toContain(':is-valid-connection="isValidConnection"')
    expect(editor).toContain('@connect-start="startConnection"')
    expect(editor).toContain('@connect-end="endConnection"')
    expect(canvas).toContain('<WorkflowConnectionMenu')
    expect(editor).toContain('useWorkflowConnectionAuthoring({')
    expect(authoring).toContain('options.session.insertConnectedNode(')
    expect(edges).toContain('targetHandle: targetHandle(edge)')
    expect(editor).not.toContain('if (source.channel !== target.channel) return null')
    expect(menu).toContain('@keydown="onListKeydown"')
    expect(menu).toContain('numberedSelectionIndex(event, visibleCandidates.value.length)')
    expect(menu).toContain('index < NUMBERED_SELECTION_LIMIT')
    expect(menu).toContain("t('workflow.connection.show_compatible')")
    expect(menu).not.toContain('workflow.connection.keyboard_hint')
  })

  it('restores multi-selection, atomic batch editing, snapping, and auto-layout', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const canvasLayout = readSource('src/app/editor/EditorCanvasLayoutController.ts')
    const canvasGestures = readSource('src/app/editor/useWorkflowCanvasGestures.ts')
    const canvas = readSource('src/app/editor/WorkflowEditorCanvas.vue')
    expect(editor).toContain('@nodes-change="handleNodesChange"')
    expect(canvas).toContain('<WorkflowSelectionToolbar')
    expect(canvasGestures).toContain('marqueeActive')
    expect(canvasGestures).toContain('if (!marqueeActive) return')
    expect(editor).toContain('createEditorCanvasLayoutController({')
    expect(canvasLayout).toContain("applyCommand({ kind: 'move-nodes'")
    expect(canvasLayout).toContain('snapNodePosition(')
    expect(canvasLayout).toContain('autoLayoutNodePositions(')
    const selection = readSource('src/app/editor/EditorSelectionController.ts')
    expect(selection).toContain('session.duplicateNodes(')
    const selectionToolbar = readSource('src/app/editor/WorkflowSelectionToolbar.vue')
    expect(selectionToolbar).toContain('shrink-0 whitespace-nowrap')
  })

  it('projects subgraph boundaries from the canonical Source interface', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const boundary = readSource('src/app/editor/workflowGraphBoundary.ts')
    const authoring = readSource('src/app/editor/useWorkflowConnectionAuthoring.ts')
    const panel = readSource('src/app/editor/WorkflowGraphInterfacePanel.vue')
    const canvas = readSource('src/app/editor/WorkflowEditorCanvas.vue')

    expect(canvas).toContain('<WorkflowGraphBoundary')
    expect(editor).toContain('<WorkflowGraphInterfacePanel')
    expect(authoring).toContain('options.session.bindGraphBoundary(boundary)')
    expect(boundary).toContain("type: 'graph-boundary'")
    expect(boundary).not.toContain("kind: 'add-node'")
    expect(panel).toContain('graph.entries')
    expect(panel).toContain('graph.exits')
  })

  it('keeps a new subgraph editable while explaining when interface refresh is unavailable', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const boundary = readSource('src/app/editor/WorkflowGraphBoundary.vue')
    const panel = readSource('src/app/editor/WorkflowGraphInterfacePanel.vue')
    const canvas = readSource('src/app/editor/WorkflowEditorCanvas.vue')

    expect(editor).toContain('canInferGraphInterface')
    expect(editor).toContain(':infer-disabled="!canInferGraphInterface.valid"')
    expect(panel).toContain(':disabled="inferDisabled"')
    expect(canvas).toContain("graphKind === 'main'")
    expect(canvas).toContain('data-testid="workflow-subgraph-empty-hint"')
    expect(canvas).toContain('pointer-events-none')
    expect(boundary).toContain('min-w-0 truncate')
  })

  it('restores source-native node search and canvas focus', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const keyboard = readSource('src/app/editor/editorKeyboard.ts')
    const toolbarModel = readSource('src/app/editor/editorToolbarModel.ts')
    const nodeSearch = readSource('src/app/editor/useWorkflowNodeSearch.ts')
    expect(toolbarModel).toContain("testId: 'workflow-find-node'")
    expect(keyboard).toContain("if (key === 'f')")
    expect(editor).toContain('useWorkflowNodeSearch({')
    expect(nodeSearch).toContain('options.source.value?.graphs')
    expect(nodeSearch).toContain('await options.focusNode([result.graphId], result.nodeId)')
  })

  it('opens contextual quick add from Tab and inserts snippets through safe shortcuts', () => {
    const editor = readSource('src/views/WorkflowEditorView.vue')
    const keyboard = readSource('src/app/editor/editorKeyboard.ts')
    const quickAdd = readSource('src/app/editor/WorkflowQuickAddMenu.vue')
    const snippetModal = readSource('src/app/editor/WorkflowSnippetModal.vue')
    const dialogs = readSource('src/app/editor/WorkflowEditorDialogs.vue')
    const canvas = readSource('src/app/editor/WorkflowEditorCanvas.vue')
    expect(dialogs).toContain('<WorkflowQuickAddMenu')
    expect(keyboard).toContain("event.key === 'Tab'")
    expect(canvas).toContain("emit('track-pointer', $event)")
    expect(editor).toContain('shortcutFromKeyboardEvent(event)')
    expect(editor).toContain('useSnippet(action.snippetID, canvasInsertionPosition())')
    expect(quickAdd).toContain('workflow-quick-add-search')
    expect(quickAdd).toContain('@keydown="onListKeydown"')
    expect(quickAdd).toContain('numberedSelectionIndex(event, visibleItems.value.length)')
    expect(quickAdd).toContain('index < NUMBERED_SELECTION_LIMIT')
    expect(quickAdd).not.toContain('{{ item.categoryLabel }}')
    expect(quickAdd).toContain('<Teleport to="body">')
    expect(quickAdd).toContain('@mouseenter="previewCategory(entry.value)"')
    expect(dialogs).toContain(':anchor="quickAddAnchor"')
    expect(snippetModal).toContain('<HotkeyCaptureInput')
  })

  it('keeps edge highlighting, additive selection, and batch insertion visible in the editor', () => {
    const quickAdd = readSource('src/app/editor/useWorkflowQuickAdd.ts')
    const selection = readSource('src/app/editor/EditorSelectionController.ts')
    const styles = readSource('src/views/WorkflowEditorView.css')
    const edges = readSource('src/app/editor/useWorkflowEdgeInteractions.ts')
    expect(edges).toContain('selectedEdgeIds.value.has(edgeId(edge))')
    expect(edges).toContain('source?.ctrlKey || source?.metaKey')
    expect(quickAdd).toContain('session.insertNodesIntoSignalEdges(')
    expect(selection).toContain("kind: 'select-edge'")
    expect(styles).toContain('.vue-flow__edge.selected .vue-flow__edge-path')
  })

  it('uses the viewport-native context menu instead of a node-local hidden anchor', () => {
    const node = readSource('src/app/editor/WorkflowNode.vue')
    expect(node).toContain('<Teleport to="body">')
    expect(node).toContain('class="pointer-events-none fixed size-px opacity-0"')
    expect(node).toContain('x: event.clientX, y: event.clientY')
    expect(node).not.toContain('clientX - bounds.left')
  })

  it('keeps region fields readable and makes state initial values editable', () => {
    const region = readSource('src/app/editor/RegionValueEditor.vue')
    const state = readSource('src/app/editor/WorkflowStatePanel.vue')
    const valueEditor = readSource('src/app/editor/WorkflowValueEditor.vue')

    expect(region).toContain('grid-cols-2')
    expect(region).not.toContain('grid-cols-4')
    expect(valueEditor).toContain(":size=\"compact ? 'xs' : 'sm'\"")
    expect(state).toContain('<StateDefaultValueEditor')
    expect(state).toContain('newVariableDefault')
    expect(state).toContain('updateVariableDefault(variable, $event)')
  })
})
