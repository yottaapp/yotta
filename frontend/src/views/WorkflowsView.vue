<template>
  <div class="workspace-page workspace-canvas flex h-full min-h-0 w-full flex-col overflow-hidden">
    <header
      class="workspace-page__header flex min-h-[72px] shrink-0 items-center justify-between gap-6 px-8 py-4 max-[900px]:flex-col max-[900px]:items-start max-[900px]:px-6"
    >
      <div class="min-w-0">
        <div class="flex items-center gap-3">
          <span
            class="workspace-page__mark flex size-10 shrink-0 items-center justify-center rounded-[10px] border border-primary/25 bg-primary/10 text-primary"
          >
            <UIcon name="i-tabler-route" class="size-5" />
          </span>
          <div class="min-w-0">
            <div class="flex min-w-0 items-center gap-2">
              <h1
                class="workspace-page__title truncate text-xl leading-tight font-semibold tracking-[-0.02em] text-highlighted"
              >
                {{ t('workflow.list.title') }}
              </h1>
              <UBadge color="neutral" variant="soft" size="sm">{{ total }}</UBadge>
            </div>
          </div>
        </div>
      </div>
      <div class="flex shrink-0 flex-wrap items-center justify-end gap-2">
        <UButton
          data-testid="workflow-new-button"
          icon="i-tabler-plus"
          :label="t('workflow.list.new_workflow')"
          @click="openCreateModal"
        />
        <UDropdownMenu :items="libraryMenuItems">
          <UButton
            color="neutral"
            variant="ghost"
            icon="i-tabler-dots-vertical"
            :aria-label="t('workflow.list.library_actions')"
          />
        </UDropdownMenu>
      </div>
    </header>

    <main
      class="flex min-h-0 flex-1 flex-col px-6 py-4"
      data-testid="workflow-library"
      data-mode="manage"
      :data-total="total"
    >
      <section
        v-if="recoveries.length"
        class="mb-3 shrink-0 rounded-lg border border-warning/35 bg-warning/10 px-3 py-2"
        role="alert"
        data-testid="workflow-recovery-panel"
      >
        <div class="flex items-center gap-2">
          <UIcon name="i-tabler-first-aid-kit" class="size-4 shrink-0 text-warning" />
          <p class="min-w-0 flex-1 text-xs text-default">
            {{ t('workflow.list.recovery_title', { n: recoveries.length }) }}
          </p>
          <UButton
            size="xs"
            color="neutral"
            variant="ghost"
            :icon="recoveryExpanded ? 'i-tabler-chevron-up' : 'i-tabler-chevron-down'"
            :label="
              t(recoveryExpanded ? 'workflow.list.recovery_hide' : 'workflow.list.recovery_review')
            "
            @click="recoveryExpanded = !recoveryExpanded"
          />
        </div>
        <ul v-if="recoveryExpanded" class="mt-2 grid gap-2 xl:grid-cols-2">
          <li
            v-for="recovery in recoveries"
            :key="recovery.recoveryId"
            class="flex items-center gap-2 rounded-md border border-default bg-default/70 px-3 py-2"
          >
            <div class="min-w-0 flex-1">
              <p class="truncate text-xs font-medium text-default">{{ recovery.originalName }}</p>
              <p class="truncate text-[10px] text-dimmed">{{ recovery.reason }}</p>
            </div>
            <UButton
              size="xs"
              color="neutral"
              variant="soft"
              icon="i-tabler-tool"
              :label="t('workflow.list.recovery_repair')"
              @click="openRecoveryRepair(recovery)"
            />
            <UButton
              size="xs"
              color="error"
              variant="ghost"
              icon="i-tabler-trash"
              :aria-label="t('workflow.list.recovery_delete')"
              :loading="recoveryBusyId === recovery.recoveryId"
              @click="deleteRecovery(recovery)"
            />
          </li>
        </ul>
      </section>

      <section
        class="workspace-surface shrink-0 overflow-hidden rounded-t-lg border border-default"
      >
        <form
          class="flex items-center gap-2 border-b border-default p-3"
          role="search"
          @submit.prevent="applySearch"
        >
          <UInput
            v-model="searchInput"
            icon="i-tabler-search"
            :placeholder="t('workflow.list.search_all_placeholder')"
            class="min-w-0 flex-1"
          />
          <UButton type="submit" color="neutral" variant="soft" icon="i-tabler-search">
            {{ t('workflow.list.search_action') }}
          </UButton>
        </form>
        <LibrarySelectionToolbar
          v-if="selectedRows.length"
          :label="t('workflow.list.selected_count', { n: selectedRows.length })"
          :hint="t('batchMetadata.selection_hint')"
          :clear-label="t('workflow.list.clear_selection')"
          @clear="clearSelection"
        >
          <UButton
            data-testid="workflow-batch-metadata"
            size="sm"
            variant="soft"
            icon="i-tabler-category-plus"
            :disabled="batchBusy"
            @click="openBatchEdit"
          >
            {{ t('workflow.list.batch_edit') }}
          </UButton>
          <UButton
            size="sm"
            color="neutral"
            variant="ghost"
            icon="i-tabler-file-export"
            :loading="batchExporting"
            :disabled="portabilityBusy"
            @click="exportSelected"
          >
            {{ t('workflow.list.export_selected') }}
          </UButton>
          <template #destructive>
            <UButton
              size="sm"
              color="error"
              variant="ghost"
              icon="i-tabler-trash"
              :loading="deleting"
              @click="requestDelete(selectedRows)"
            >
              {{ t('workflow.list.delete_selected') }}
            </UButton>
          </template>
        </LibrarySelectionToolbar>
        <div v-else class="flex flex-wrap items-center gap-2 p-3">
          <AdaptiveSelect
            v-model="categoryFilter"
            :items="categoryFilterItems"
            class="shrink-0"
            icon="i-tabler-category"
            @update:model-value="queryChanged"
          />
          <UInputMenu
            v-model="tagFilters"
            :items="tagOptions"
            multiple
            class="min-w-56 max-w-md flex-1"
            icon="i-tabler-tags"
            :placeholder="t('workflow.list.all_tags')"
            @update:model-value="queryChanged"
          />
          <AdaptiveSelect
            v-model="createdRange"
            :items="createdRangeItems"
            class="shrink-0"
            icon="i-tabler-calendar-plus"
            @update:model-value="queryChanged"
          />
          <AdaptiveSelect
            v-model="updatedRange"
            :items="updatedRangeItems"
            class="shrink-0"
            icon="i-tabler-calendar-stats"
            @update:model-value="queryChanged"
          />
          <AdaptiveSelect
            v-model="sort"
            :items="sortItems"
            class="shrink-0"
            icon="i-tabler-arrows-sort"
            @update:model-value="queryChanged"
          />
          <UDropdownMenu :items="columnMenuItems">
            <UButton
              color="neutral"
              variant="soft"
              icon="i-tabler-columns-3"
              trailing-icon="i-tabler-chevron-down"
              :label="t('workflow.list.columns')"
            />
          </UDropdownMenu>
          <UButton
            v-if="hasFilters"
            color="neutral"
            variant="ghost"
            icon="i-tabler-filter-x"
            :label="t('workflow.list.reset_filters')"
            @click="resetFilters"
          />
        </div>
      </section>

      <div
        v-if="portabilityFeedback || deleteFeedback"
        class="shrink-0 border-x border-b px-4 py-2 text-xs"
        :class="feedbackClass"
        :role="activeFeedback?.tone === 'error' ? 'alert' : 'status'"
      >
        <p>{{ activeFeedback?.message }}</p>
        <ul v-if="activeFeedback?.details.length" class="mt-1 list-disc pl-5 text-[11px]">
          <li v-for="detail in activeFeedback.details" :key="detail">{{ detail }}</li>
        </ul>
      </div>

      <div class="workspace-surface min-h-0 flex-1 overflow-auto border-x border-default">
        <div v-if="loading" class="space-y-px p-2" :aria-label="t('workflow.list.loading')">
          <USkeleton v-for="index in 10" :key="index" class="h-14 rounded-md" />
        </div>
        <div
          v-else-if="failure"
          class="m-3 flex items-center gap-3 rounded-lg border border-error/35 bg-error/10 px-4 py-3 text-sm text-error"
          role="alert"
        >
          <span class="min-w-0 flex-1">{{ failure }}</span>
          <UButton size="xs" color="error" variant="soft" @click="load">{{
            t('common.retry')
          }}</UButton>
        </div>
        <EmptyState
          v-else-if="sources.length === 0"
          inset
          :icon="hasFilters ? 'i-tabler-filter-off' : 'i-tabler-route-off'"
          :title="t(hasFilters ? 'workflow.list.no_results_title' : 'workflow.list.empty_title')"
          :description="
            t(
              hasFilters
                ? 'workflow.list.no_results_description'
                : 'workflow.list.empty_description',
            )
          "
        >
          <template #action>
            <UButton
              v-if="hasFilters"
              color="neutral"
              variant="soft"
              icon="i-tabler-filter-x"
              :label="t('workflow.list.reset_filters')"
              @click="resetFilters"
            />
            <UButton
              v-else
              icon="i-tabler-plus"
              :label="t('workflow.list.new_workflow')"
              @click="openCreateModal"
            />
          </template>
        </EmptyState>
        <div v-else class="min-w-[1100px]" data-testid="workflow-management-table">
          <div
            class="workspace-surface-strong grid h-9 items-center gap-3 border-b border-default px-3 text-[10px] font-semibold uppercase tracking-wide text-dimmed"
            :style="{ gridTemplateColumns: workflowGridTemplate }"
          >
            <UCheckbox
              :model-value="allCurrentPageSelected"
              :aria-label="t('workflow.list.select_page')"
              @update:model-value="toggleCurrentPage(Boolean($event))"
            />
            <span>{{ t('workflow.list.name') }}</span>
            <span v-if="isColumnVisible('category')">{{ t('workflow.list.category') }}</span>
            <span v-if="isColumnVisible('tags')">{{ t('workflow.list.tags') }}</span>
            <span v-if="isColumnVisible('nodes')" class="text-right">{{
              t('workflow.list.nodes')
            }}</span>
            <span v-if="isColumnVisible('revision')" class="text-right">{{
              t('workflow.list.revision')
            }}</span>
            <span v-if="isColumnVisible('createdAt')">{{ t('workflow.list.created_at') }}</span>
            <span v-if="isColumnVisible('updatedAt')">{{ t('workflow.list.updated_at') }}</span>
            <span class="text-right">{{ t('workflow.list.actions') }}</span>
          </div>
          <article
            v-for="source in sources"
            :key="source.workflowId"
            class="workspace-table-row grid min-h-16 items-center gap-3 border-b border-default/70 px-3 py-2 transition-colors duration-150 hover:bg-[var(--ui-surface-hover)]"
            :style="{ gridTemplateColumns: workflowGridTemplate }"
            data-testid="workflow-library-row"
            :data-workflow-id="source.workflowId"
            @dblclick="openWorkflow(source.workflowId)"
          >
            <UCheckbox
              :model-value="Boolean(selected[source.workflowId])"
              :aria-label="t('workflow.list.select_named', { name: source.name })"
              @update:model-value="toggleSource(source, Boolean($event))"
              @dblclick.stop
            />
            <div class="min-w-0">
              <div class="flex min-w-0 items-center gap-2">
                <button
                  class="flex min-w-0 items-center gap-2 text-left text-sm font-medium text-highlighted"
                  :aria-expanded="Boolean(expandedParameters[source.workflowId])"
                  @click="toggleParameters(source.workflowId)"
                  @dblclick.stop="openWorkflow(source.workflowId)"
                >
                  <UIcon
                    :name="
                      expandedParameters[source.workflowId]
                        ? 'i-tabler-chevron-down'
                        : 'i-tabler-chevron-right'
                    "
                    class="size-4 shrink-0 text-muted"
                  /><span class="truncate">{{ source.name }}</span>
                </button>
                <UBadge
                  v-if="runFeedbackById[source.workflowId]"
                  :color="runFeedbackById[source.workflowId].tone"
                  variant="soft"
                  size="xs"
                >
                  {{ runFeedbackById[source.workflowId].label }}
                </UBadge>
              </div>
              <p class="mt-0.5 truncate text-[11px] text-muted">
                {{ source.description || t('workflow.list.no_description') }}
              </p>
              <p class="mt-0.5 truncate font-mono text-[9px] text-dimmed">
                {{ source.workflowId }}
              </p>
            </div>
            <div v-if="isColumnVisible('category')" class="min-w-0">
              <UBadge v-if="source.category" color="neutral" variant="soft" size="sm">{{
                source.category
              }}</UBadge>
              <span v-else class="text-[11px] text-dimmed">{{
                t('workflow.list.unclassified')
              }}</span>
            </div>
            <div
              v-if="isColumnVisible('tags')"
              class="flex min-w-0 items-center gap-1 overflow-hidden"
            >
              <UBadge
                v-for="tag in (source.tags ?? []).slice(0, 3)"
                :key="tag"
                color="neutral"
                variant="subtle"
                size="sm"
              >
                {{ tag }}
              </UBadge>
              <span v-if="!(source.tags ?? []).length" class="text-[11px] text-dimmed">{{
                t('workflow.list.no_tags')
              }}</span>
              <span v-else-if="(source.tags ?? []).length > 3" class="text-[10px] text-dimmed"
                >+{{ (source.tags ?? []).length - 3 }}</span
              >
            </div>
            <span v-if="isColumnVisible('nodes')" class="text-right font-mono text-xs text-muted">{{
              source.nodeCount
            }}</span>
            <span
              v-if="isColumnVisible('revision')"
              class="text-right font-mono text-xs text-muted"
              >{{ source.revision }}</span
            >
            <time
              v-if="isColumnVisible('createdAt')"
              :datetime="source.createdAt || undefined"
              class="text-xs text-muted"
              :title="source.createdAt ? formatExactDate(source.createdAt) : undefined"
            >
              {{ formatListDate(source.createdAt) }}
            </time>
            <time
              v-if="isColumnVisible('updatedAt')"
              :datetime="source.updatedAt || undefined"
              class="text-xs text-muted"
              :title="source.updatedAt ? formatExactDate(source.updatedAt) : undefined"
            >
              {{ formatListDate(source.updatedAt) }}
            </time>
            <div class="flex justify-end gap-1" @dblclick.stop>
              <UButton
                icon="i-tabler-adjustments-horizontal"
                color="neutral"
                variant="ghost"
                size="sm"
                :aria-label="t('workflow.parameters.run_title')"
                @click="toggleParameters(source.workflowId)"
                :aria-expanded="Boolean(expandedParameters[source.workflowId])"
              />
              <UButton
                v-if="activeRunIdByWorkflow[source.workflowId]"
                data-testid="workflow-stop"
                icon="i-tabler-square"
                color="error"
                variant="ghost"
                size="sm"
                :aria-label="t('workflow.action.stop_named', { name: source.name })"
                :loading="runStartingId === source.workflowId"
                :disabled="Boolean(runStartingId) || deleting"
                @click="stopWorkflow(source.workflowId)"
              />
              <UButton
                v-else
                data-testid="workflow-run"
                icon="i-tabler-player-play"
                color="neutral"
                variant="ghost"
                size="sm"
                :aria-label="t('workflow.action.run_named', { name: source.name })"
                :loading="runStartingId === source.workflowId"
                :disabled="Boolean(runStartingId) || deleting"
                @click="runFromRow(source.workflowId)"
              />
              <UDropdownMenu :items="rowMenuItems(source)">
                <UButton
                  data-testid="workflow-row-menu"
                  icon="i-tabler-dots"
                  color="neutral"
                  variant="ghost"
                  size="sm"
                  :aria-label="t('workflow.list.row_actions', { name: source.name })"
                />
              </UDropdownMenu>
            </div>
            <WorkflowParameterInline
              v-if="visitedParameters[source.workflowId]"
              :ref="
                (el) => {
                  if (el)
                    parameterForms.set(
                      source.workflowId,
                      el as unknown as { run: () => Promise<void> },
                    )
                  else parameterForms.delete(source.workflowId)
                }
              "
              v-model:open="expandedParameters[source.workflowId]"
              class="col-span-full -mx-3 -mb-2"
              :workflow-id="source.workflowId"
              :source-revision="source.revision"
              :name="source.name"
              @run="runWorkflow"
            />
          </article>
        </div>
      </div>

      <footer
        v-if="!loading && total > 0"
        class="workspace-surface flex min-h-14 shrink-0 items-center gap-4 rounded-b-lg border border-default px-3"
      >
        <p class="mr-auto text-xs text-dimmed">
          {{ t('workflow.list.result_range', { start: resultStart, end: resultEnd, total }) }}
        </p>
        <UPagination
          :page="page"
          :total="total"
          :items-per-page="pageSize"
          :sibling-count="1"
          active-variant="subtle"
          show-edges
          @update:page="goToPage"
        />
        <span class="text-xs text-dimmed">{{ t('workflow.list.per_page') }}</span>
        <AdaptiveSelect
          v-model="pageSize"
          :items="pageSizeItems"
          class="w-24"
          width-mode="fixed"
          @update:model-value="queryChanged"
        />
      </footer>
    </main>

    <BaseModal
      :open="publishAuthOpen"
      :title="t('workflow.market.login_before_publish')"
      icon="i-tabler-login"
      :dismissible="false"
      :show-close="false"
    >
      <div data-testid="workflow-publish-login" class="space-y-4">
        <p class="text-sm text-muted" role="status">{{ t('workflow.market.waiting_login') }}</p>
        <p v-if="publishAuthFailure" role="alert" class="whitespace-pre-wrap text-sm text-error">
          {{ publishAuthFailure }}
        </p>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="ghost" @click="publicationEntry.cancel()">{{
            t('common.cancel')
          }}</UButton>
          <UButton
            v-if="!publishAuthBusy"
            @click="pendingPublishSource && openPublish(pendingPublishSource)"
            >{{ t('common.retry') }}</UButton
          >
        </div>
      </div>
    </BaseModal>

    <BaseModal
      v-model:open="publishOpen"
      :title="t('workflow.market.publish_title')"
      icon="i-tabler-cloud-upload"
      size="2xl"
      :dismissible="false"
      :show-close="!publishing"
    >
      <WorkflowPublicationStatus v-if="publishResult" :status="publishResult" />
      <div v-else class="space-y-4">
        <WorkflowPublicationStatus
          v-if="latestSubmission"
          :status="latestSubmission.status"
          :reason="latestSubmission.reason"
        />
        <div class="flex items-center gap-3">
          <UPopover>
            <button
              type="button"
              data-testid="workflow-publish-icon"
              :disabled="publishing || publishLoading || publishHistoryFailed"
              :aria-label="t('workflow.market.choose_icon')"
              class="group relative flex size-14 shrink-0 items-center justify-center rounded-xl border border-default bg-muted text-primary hover:border-primary focus-visible:outline-2 focus-visible:outline-primary"
            >
              <WorkflowMarketIcon
                :name="publishDraft.listing.icon || 'i-tabler-route'"
                class="size-8"
              />
              <UIcon name="i-tabler-pencil" class="absolute bottom-1 right-1 size-3 text-muted" />
            </button>
            <template #content
              ><div class="w-80 p-3">
                <IconPicker
                  v-model="publishDraft.listing.icon"
                  @update:model-value="publishTouched.add('icon')"
                /></div
            ></template>
          </UPopover>
          <div class="min-w-0">
            <p class="truncate text-sm font-semibold text-highlighted">
              {{ publishDraft.title || publishSource?.name }}
            </p>
            <p class="mt-1 text-xs text-muted">
              {{
                t(
                  publishLoading
                    ? 'workflow.market.loading_previous'
                    : 'workflow.market.publish_description',
                )
              }}
            </p>
          </div>
        </div>
        <div class="flex gap-5 border-b border-default">
          <button
            v-for="section in publishSections"
            :key="section.value"
            type="button"
            :aria-pressed="publishSection === section.value"
            class="border-b-2 px-1 py-2.5 text-sm"
            :class="
              publishSection === section.value
                ? 'border-primary text-highlighted'
                : 'border-transparent text-muted'
            "
            @click="publishSection = section.value"
          >
            {{ section.label }}
          </button>
        </div>
        <div v-show="publishSection === 'listing'" class="space-y-4">
          <WorkflowPublicationPricing
            v-model="salesDraft"
            :disabled="publishing || publishLoading || publishHistoryFailed"
          />
          <UFormField :label="t('workflow.market.publish_name')" required
            ><UInput
              v-model="publishDraft.title"
              @update:model-value="publishTouched.add('title')"
              data-testid="workflow-publish-title"
              :disabled="publishing || publishLoading || publishHistoryFailed"
              class="w-full"
              maxlength="160"
          /></UFormField>
          <UFormField :label="t('workflow.market.summary')" required
            ><UTextarea
              v-model="publishDraft.summary"
              @update:model-value="publishTouched.add('summary')"
              data-testid="workflow-publish-summary"
              maxlength="1000"
              :rows="2"
              :disabled="publishing || publishLoading || publishHistoryFailed"
              class="w-full"
              :placeholder="t('workflow.market.summary_hint')"
          /></UFormField>
          <div class="grid gap-4 md:grid-cols-[minmax(12rem,18rem)_minmax(0,1fr)]">
            <UFormField :label="t('workflow.market.category')" required
              ><MarketCategorySelect
                v-model="publishDraft.listing.category"
                @update:model-value="publishTouched.add('category')"
                data-testid="workflow-publish-category"
                :categories="publishCategoryDirectory"
                active-only
                :disabled="publishing || publishLoading || publishHistoryFailed"
                class="w-full"
                :placeholder="t('workflow.market.category_select')"
            /></UFormField>
            <UFormField :label="t('workflow.market.tags')"
              ><UInputTags
                v-model="publishDraft.listing.tags"
                @update:model-value="publishTouched.add('tags')"
                data-testid="workflow-publish-tags"
                :disabled="publishing || publishLoading || publishHistoryFailed"
                class="w-full"
                :max="16"
            /></UFormField>
          </div>
          <WorkflowDimensionSelect
            v-model="publishDraft.listing.filterValues"
            :dimensions="publishFilterDimensions"
            assignment
            @update:model-value="publishTouched.add('filterValues')"
          />
          <section
            v-if="publishBundle?.panels?.length"
            class="space-y-2 rounded-lg border border-default p-3"
            data-testid="publish-panel-resources"
          >
            <h3 class="text-sm font-medium">{{ t('panels.bundle_title') }}</h3>
            <p class="text-xs text-muted">{{ t('panels.bundle_hint') }}</p>
            <div
              v-for="panel in publishBundle.panels"
              :key="panel.id"
              class="flex items-center justify-between gap-3 text-sm"
            >
              <span>{{ te(panel.title) ? t(panel.title) : panel.title }}</span>
              <span class="text-xs text-muted">{{
                panel.plugin
                  ? t('panels.bundle_plugin')
                  : t('panels.bundle_components', { count: panel.componentCount })
              }}</span>
            </div>
          </section>
        </div>
        <div v-show="publishSection === 'guide'" class="space-y-4">
          <UFormField
            :label="t('workflow.market.description')"
            :description="t('workflow.market.markdown_hint')"
            ><WorkflowMarkdownEditor
              v-model="publishDraft.listing.description"
              @update:model-value="publishTouched.add('description')"
              data-testid="workflow-publish-description"
              :label="t('workflow.market.description')"
              :disabled="publishing || publishLoading || publishHistoryFailed"
              class="w-full"
              :max-chars="32768"
          /></UFormField>
          <UFormField :label="t('workflow.market.instructions')"
            ><WorkflowMarkdownEditor
              v-model="publishDraft.listing.instructions"
              @update:model-value="publishTouched.add('instructions')"
              data-testid="workflow-publish-instructions"
              :label="t('workflow.market.instructions')"
              :disabled="publishing || publishLoading || publishHistoryFailed"
              class="w-full"
              :max-chars="16384"
          /></UFormField>
        </div>
        <div v-show="publishSection === 'release'" class="space-y-5">
          <UFormField
            :label="t('workflow.market.version')"
            required
            :error="
              publishVersionValid
                ? undefined
                : t(
                    validReleaseVersion(publishDraft.releaseVersion)
                      ? 'workflow.market.version_must_increase'
                      : 'workflow.market.invalid_version',
                  )
            "
            ><WorkflowVersionInput
              v-model="publishDraft.releaseVersion"
              @update:model-value="publishTouched.add('version')"
              :invalid="!publishVersionValid"
              :previous="publishHighestVersion"
              :disabled="publishing || publishLoading || publishHistoryFailed"
          /></UFormField>
          <p class="text-xs leading-5 text-muted">{{ t('workflow.market.version_hint') }}</p>
          <UFormField :label="t('workflow.market.release_notes')"
            ><WorkflowMarkdownEditor
              v-model="publishDraft.releaseNotes"
              data-testid="workflow-publish-release-notes"
              :label="t('workflow.market.release_notes')"
              :max-chars="20000"
              :disabled="publishing || publishLoading || publishHistoryFailed"
              class="w-full"
              :placeholder="t('workflow.market.release_notes_placeholder')"
          /></UFormField>
        </div>
        <p
          v-if="publishFailure"
          class="whitespace-pre-wrap text-sm leading-6 text-error"
          role="alert"
        >
          {{ publishFailure }}
        </p>
        <UButton
          v-if="publishNeedsLogin"
          data-testid="workflow-publish-reauth"
          icon="i-tabler-login"
          :loading="publishing"
          @click="recoverPublishLogin"
          >{{ t('workflow.market.login_keep_draft') }}</UButton
        >
        <p v-if="publishNeedsLogin && publishing" role="status" class="text-sm text-muted">
          {{ t('workflow.market.waiting_login') }}
        </p>
        <UButton
          v-if="publishHistoryFailed"
          color="neutral"
          variant="outline"
          @click="publishSource && openPublish(publishSource)"
          >{{ t('common.retry') }}</UButton
        >
      </div>
      <template #footer>
        <span class="mr-auto text-xs tabular-nums text-muted">{{
          publishDraft.releaseVersion
        }}</span>
        <UButton color="neutral" variant="ghost" @click="cancelPublish">{{
          t(publishResult ? 'common.close' : 'common.cancel')
        }}</UButton>
        <UButton
          v-if="publishResult || latestSubmission"
          color="neutral"
          @click="openPublicationHistory"
          >{{ t('workflow.market.my_submissions') }}</UButton
        >
        <p v-if="publishResult && publishFailure" role="alert" class="text-error">
          {{ publishFailure }}
        </p>
        <UButton
          v-if="!publishResult"
          data-testid="workflow-publish-submit"
          icon="i-tabler-cloud-upload"
          :loading="publishing || publishLoading"
          :disabled="
            publishLoading ||
            publishHistoryFailed ||
            !publishVersionValid ||
            !validSalesDraft(salesDraft) ||
            latestSubmission?.status === 'pending_review' ||
            publishNeedsLogin ||
            !publishCategoryValid ||
            !publishMarkdownValid ||
            !publishDraft.title.trim() ||
            !publishDraft.summary.trim()
          "
          @click="publishWorkflow"
          >{{ t('workflow.market.publish_action') }}</UButton
        >
      </template>
    </BaseModal>

    <BaseModal
      v-model:open="batchEditing"
      :title="t('workflow.list.batch_edit_title', { n: selectedRows.length })"
      icon="i-tabler-category-plus"
      size="lg"
      :dismissible="!batchBusy"
    >
      <div class="space-y-5">
        <p class="text-sm text-muted">{{ t('batchMetadata.description') }}</p>
        <UFormField :label="t('common.category')">
          <div class="flex items-center gap-2">
            <AdaptiveSelect
              v-model="batchDraft.categoryMode"
              :items="categoryModeItems"
              class="w-36 shrink-0"
              width-mode="fixed"
            />
            <UInputMenu
              v-if="batchDraft.categoryMode === 'set'"
              v-model="batchDraft.category"
              :items="batchCategoryOptions"
              :create-item="'always'"
              :placeholder="t('workflow.list.category_placeholder')"
              class="min-w-0 flex-1"
              @create="createBatchCategory"
            />
            <span v-else class="text-xs text-dimmed">{{ categoryModeHint }}</span>
          </div>
        </UFormField>
        <UFormField :label="t('common.tags')">
          <div class="flex items-start gap-2">
            <AdaptiveSelect
              v-model="batchDraft.tagMode"
              :items="tagModeItems"
              class="w-36 shrink-0"
              width-mode="fixed"
            />
            <UInputMenu
              v-if="tagModeNeedsValues"
              v-model="batchDraft.tags"
              :items="batchTagOptions"
              :create-item="'always'"
              multiple
              :placeholder="t('workflow.list.tags_placeholder')"
              class="min-w-0 flex-1"
              @create="createBatchTag"
            />
            <span v-else class="pt-2 text-xs text-dimmed">{{ tagModeHint }}</span>
          </div>
        </UFormField>
      </div>
      <template #footer>
        <UButton
          color="neutral"
          variant="ghost"
          :disabled="batchBusy"
          @click="batchEditing = false"
        >
          {{ t('common.cancel') }}
        </UButton>
        <UButton
          icon="i-tabler-check"
          :label="t('batchMetadata.apply')"
          :loading="batchBusy"
          :disabled="!batchDraftValid"
          @click="saveBatchMetadata"
        />
      </template>
    </BaseModal>

    <BaseModal
      v-model:open="metadataModalOpen"
      :title="t(editingSource ? 'workflow.list.edit_metadata_title' : 'workflow.list.create_title')"
      :icon="editingSource ? 'i-tabler-edit' : 'i-tabler-plus'"
      size="2xl"
      :dismissible="!metadataBusy"
    >
      <form class="grid gap-4" @submit.prevent="saveMetadata">
        <UFormField :label="t('workflow.list.name')" required>
          <UInput
            v-model="metadataDraft.name"
            data-testid="workflow-create-name"
            :placeholder="t('workflow.list.name_placeholder')"
            autofocus
          />
        </UFormField>
        <UFormField v-if="!editingSource" :label="t('workflow.list.template_label')">
          <AdaptiveSelect v-model="metadataDraft.template" :items="templateItems" :max-width="32" />
        </UFormField>
        <UFormField :label="t('workflow.list.description_label')">
          <UTextarea
            v-model="metadataDraft.description"
            :rows="3"
            :placeholder="t('workflow.list.description_placeholder')"
          />
        </UFormField>
        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField :label="t('workflow.list.category')">
            <UInputMenu
              v-model="metadataDraft.category"
              :items="metadataCategoryOptions"
              :create-item="'always'"
              :placeholder="t('workflow.list.category_placeholder')"
              @create="createMetadataCategory"
            />
          </UFormField>
          <UFormField :label="t('workflow.list.tags')">
            <UInputMenu
              v-model="metadataDraft.tags"
              :items="metadataTagOptions"
              :create-item="'always'"
              multiple
              :placeholder="t('workflow.list.tags_placeholder')"
              @create="createMetadataTag"
            />
          </UFormField>
        </div>
        <WorkflowHotkeyField
          v-if="editingSource"
          :workflow-id="editingSource.workflowId"
          :name="metadataDraft.name || editingSource.name"
        />
        <p v-if="metadataFailure" class="text-sm text-error" role="alert">{{ metadataFailure }}</p>
      </form>
      <template #footer>
        <UButton
          color="neutral"
          variant="ghost"
          :disabled="metadataBusy"
          @click="metadataModalOpen = false"
        >
          {{ t('common.cancel') }}
        </UButton>
        <UButton
          data-testid="workflow-create-submit"
          icon="i-tabler-check"
          :label="t(editingSource ? 'common.save' : 'workflow.list.create')"
          :loading="metadataBusy"
          :disabled="!metadataDraft.name.trim()"
          @click="saveMetadata"
        />
      </template>
    </BaseModal>

    <BaseModal
      v-model:open="recoveryRepairOpen"
      :title="t('workflow.list.recovery_repair_title')"
      icon="i-tabler-first-aid-kit"
      icon-color="warning"
      size="3xl"
      :dismissible="!recoveryBusyId"
    >
      <div class="space-y-3">
        <p class="text-sm text-muted">{{ t('workflow.list.recovery_repair_description') }}</p>
        <p v-if="activeRecovery" class="text-xs text-dimmed">{{ activeRecovery.originalName }}</p>
        <UTextarea
          v-model="recoveryDraft"
          :rows="18"
          autoresize
          class="w-full font-mono text-xs"
          :disabled="Boolean(recoveryBusyId)"
          :aria-label="t('workflow.list.recovery_source_json')"
        />
        <p v-if="recoveryFailure" class="text-sm text-error" role="alert">{{ recoveryFailure }}</p>
      </div>
      <template #footer>
        <UButton
          color="neutral"
          variant="ghost"
          :disabled="Boolean(recoveryBusyId)"
          @click="recoveryRepairOpen = false"
        >
          {{ t('common.cancel') }}
        </UButton>
        <UButton
          color="warning"
          icon="i-tabler-tool"
          :label="t('workflow.list.recovery_validate_repair')"
          :loading="Boolean(recoveryBusyId)"
          :disabled="!activeRecovery || !recoveryDraft.trim()"
          @click="repairRecovery"
        />
      </template>
    </BaseModal>
  </div>
</template>

<script setup lang="ts">
import WorkflowParameterInline from '@/components/workflow/WorkflowParameterInline.vue'
import WorkflowMarketIcon from '@/components/workflow/WorkflowMarketIcon.vue'
import {
  computed,
  defineAsyncComponent,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from 'vue'
import WorkflowVersionInput from '@/components/workflow/WorkflowVersionInput.vue'
import WorkflowPublicationPricing from '@/components/workflow/WorkflowPublicationPricing.vue'
import WorkflowPublicationStatus from '@/components/workflow/WorkflowPublicationStatus.vue'
import {
  validSalesDraft,
  publicationSales,
  publicationVersion,
  type SalesDraft,
} from '@/lib/publication'
import IconPicker from '@/components/common/IconPicker.vue'
import { usePublicationEntry } from '@/app/workflow-library/usePublicationEntry'
import { validReleaseVersion, isNewerRelease } from '@/app/workflow-library/releaseVersion'
const WorkflowMarkdownEditor = defineAsyncComponent(
  () => import('@/components/workflow/WorkflowMarkdownEditor.vue'),
)
import { shopTransport } from '@/app/transport/shop'
import { useRouter } from 'vue-router'
import { useToast } from '@/composables/useAppToast'
import { useI18n } from 'vue-i18n'
import {
  onRunChanged,
  workflowTransport,
  type BundleInfoView,
  type DeleteSourcePreview,
  type SourceRecoveryView,
  type SourceView,
} from '@/app/transport/workflow'
import { runReadinessMessage, runStartOutcome } from '@/app/run/runReadiness'
import { pollTerminalRunStatus } from '@/app/run/followRun'
import { useConfirm } from '@/composables/useConfirm'
import { useAutoDismissFeedback } from '@/composables/useAutoDismissFeedback'
import { errorMessage, normalizeError } from '@/lib/invoke'
import {
  applyBatchMetadata,
  createBatchMetadataDraft,
  hasBatchMetadataChange,
  uniqueMetadataValues,
} from '@/lib/batchMetadata'
import BaseModal from '@/components/common/BaseModal.vue'
import AdaptiveSelect from '@/components/common/AdaptiveSelect.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LibrarySelectionToolbar from '@/components/library/LibrarySelectionToolbar.vue'
import WorkflowHotkeyField from '@/components/hotkeys/WorkflowHotkeyField.vue'
import WorkflowDimensionSelect from '@/components/workflow/WorkflowDimensionSelect.vue'
import MarketCategorySelect from '@/components/workflow/MarketCategorySelect.vue'
import { categoryRows, type MarketCategory } from '@/lib/marketCategories'
import { useWorkflowLibraryQuery } from '@/app/workflow-library/useWorkflowLibraryQuery'
import {
  useWorkflowLibrarySelection,
  type SelectedWorkflowSource,
} from '@/app/workflow-library/useWorkflowLibrarySelection'

defineOptions({ name: 'WorkflowsView' })

type SelectedSource = SelectedWorkflowSource
type WorkflowColumn = 'category' | 'tags' | 'nodes' | 'revision' | 'createdAt' | 'updatedAt'
type Feedback = { tone: 'success' | 'warning' | 'error'; message: string; details: string[] }

const defaultColumns: WorkflowColumn[] = ['category', 'tags', 'nodes', 'createdAt', 'updatedAt']
const router = useRouter()
const toast = useToast()
const { t, te, locale } = useI18n()
const { confirm } = useConfirm()
const libraryQuery = useWorkflowLibraryQuery({
  reload: load,
  translate: (key, params) => t(key, params ?? {}),
})
const {
  total,
  page,
  pageSize,
  sort,
  searchInput,
  categoryFilter,
  tagFilters,
  createdRange,
  updatedRange,
  categories,
  tags,
  resultStart,
  resultEnd,
  hasFilters,
  categoryFilterItems,
  tagOptions,
  createdRangeItems,
  updatedRangeItems,
  sortItems,
  pageSizeItems,
  queryChanged,
  applySearch,
  resetFilters,
  goToPage,
} = libraryQuery
const sources = ref<SourceView[]>([])
const librarySelection = useWorkflowLibrarySelection(sources)
const {
  selected,
  rows: selectedRows,
  allCurrentPageSelected,
  toggle: toggleSource,
  toggleCurrentPage,
  clear: clearSelection,
  retainOnly: retainFailedWorkflowSelection,
  remove: removeWorkflowSelection,
  name: selectedName,
} = librarySelection
const recoveries = ref<SourceRecoveryView[]>([])
const recoveryExpanded = ref(false)
const visibleColumns = ref<WorkflowColumn[]>(loadColumns())
const loading = ref(true)
const deleting = ref(false)
const importing = ref(false)
const exportingId = ref('')
const replacingId = ref('')
const batchExporting = ref(false)
const batchEditing = ref(false)
const batchBusy = ref(false)
const batchDraft = reactive(createBatchMetadataDraft())
const failure = ref('')
const recoveryRepairOpen = ref(false)
const recoveryDraft = ref('')
const recoveryFailure = ref('')
const recoveryBusyId = ref('')
const activeRecovery = ref<SourceRecoveryView | null>(null)
const deleteFeedback = ref<Feedback | null>(null)
const portabilityFeedback = ref<Feedback | null>(null)
useAutoDismissFeedback(deleteFeedback)
useAutoDismissFeedback(portabilityFeedback)
const runStartingId = ref('')
const activeRunIdByWorkflow = reactive<Record<string, string>>({})
const runFeedbackById = reactive<
  Record<
    string,
    { tone: 'success' | 'warning' | 'error' | 'neutral'; label: string; detail: string }
  >
>({})
const metadataModalOpen = ref(false)
const metadataBusy = ref(false)
const metadataFailure = ref('')
const editingSource = ref<SourceView | null>(null)
const expandedParameters = reactive<Record<string, boolean>>({})
const visitedParameters = reactive<Record<string, boolean>>({})
const parameterForms = new Map<string, { run: () => Promise<void> }>()
function toggleParameters(id: string) {
  visitedParameters[id] = true
  expandedParameters[id] = !expandedParameters[id]
}
function runFromRow(id: string) {
  const form = parameterForms.get(id)
  if (form) void form.run()
  else void runWorkflow(id)
}
const createdCategories = ref<string[]>([])
const createdTags = ref<string[]>([])
const metadataDraft = reactive({
  name: '',
  description: '',
  category: '',
  tags: [] as string[],
  template: 'generic' as 'generic' | 'windows' | 'android' | 'browser' | 'cross-target',
})
const publishOpen = ref(false)
const publishResult = ref('')
const latestSubmission = ref<{ submissionId: string; status: string; reason: string } | null>(null)
const salesDraft = ref<SalesDraft>({
  paid: false,
  price: '',
  revision: 0,
  available: true,
  configured: false,
})
const pendingPublishSource = ref<SourceView | null>(null)
const publishNeedsLogin = ref(false)
const publicationEntry = usePublicationEntry({
  login: () => shopTransport.login(),
  cancel: () => shopTransport.cancelLogin(),
  message: errorMessage,
})
const {
  busy: publishAuthBusy,
  open: publishAuthOpen,
  failure: publishAuthFailure,
} = publicationEntry
onBeforeUnmount(() => {
  void publicationEntry.cancel()
})
const publishing = ref(false)
const publishFailure = ref('')
const publishSource = ref<SourceView | null>(null)
const publishDraft = reactive({
  listing: {
    filterValues: [] as string[],
    icon: 'i-tabler-route',
    category: '',
    tags: [] as string[],
    description: '',
    instructions: '',
  },
  releaseVersion: '1.0.0',
  title: '',
  summary: '',
  releaseNotes: '',
})

const publishHighestVersion = ref('')
const publishVersionValid = computed(
  () =>
    validReleaseVersion(publishDraft.releaseVersion) &&
    (!publishHighestVersion.value ||
      isNewerRelease(publishDraft.releaseVersion, publishHighestVersion.value)),
)
const publishMarkdownValid = computed(
  () =>
    Array.from(publishDraft.listing.description).length <= 32768 &&
    Array.from(publishDraft.listing.instructions).length <= 16384 &&
    Array.from(publishDraft.releaseNotes).length <= 20000,
)
const publishSection = ref('listing')
const publishCategoryDirectory = ref<MarketCategory[]>([])
const publishFilterDimensions = ref<
  import('@bindings/github.com/yottaapp/yotta/internal/registryclient/models.js').FilterDimension[]
>([])
const publishCategoryOptions = computed(() =>
  categoryRows(publishCategoryDirectory.value)
    .filter((item) => item.active)
    .map((item) => ({ label: item.label, value: item.key })),
)
const publishCategoryValid = computed(() =>
  publishCategoryOptions.value.some((item) => item.value === publishDraft.listing.category),
)
const publishLoading = ref(false)
const publishBundle = ref<BundleInfoView | null>(null)
const publishHistoryFailed = ref(false)
const publishTouched = new Set<string>()
let publishGeneration = 0
const publishSections = computed(() => [
  { value: 'listing', label: t('workflow.market.listing_tab') },
  { value: 'guide', label: t('workflow.market.guide_tab') },
  { value: 'release', label: t('workflow.market.release_tab') },
])
const portabilityBusy = computed(
  () =>
    importing.value ||
    Boolean(exportingId.value) ||
    Boolean(replacingId.value) ||
    batchExporting.value ||
    batchBusy.value,
)
const activeFeedback = computed(() => portabilityFeedback.value ?? deleteFeedback.value)
const feedbackClass = computed(() => {
  const tone = activeFeedback.value?.tone
  if (tone === 'error') return 'border-error/30 bg-error/10 text-error'
  if (tone === 'warning') return 'border-warning/30 bg-warning/10 text-warning'
  return 'border-success/30 bg-success/10 text-success'
})
const metadataCategoryOptions = computed(() =>
  uniqueStrings([
    ...categories.value.map((item) => item.value),
    ...createdCategories.value,
    metadataDraft.category,
  ]),
)
const metadataTagOptions = computed(() =>
  uniqueStrings([
    ...tags.value.map((item) => item.value),
    ...createdTags.value,
    ...metadataDraft.tags,
  ]),
)
const batchCategoryOptions = computed(() =>
  uniqueStrings([
    ...categories.value.map((item) => item.value),
    ...createdCategories.value,
    batchDraft.category,
  ]),
)
const batchTagOptions = computed(() =>
  uniqueStrings([
    ...tags.value.map((item) => item.value),
    ...createdTags.value,
    ...batchDraft.tags,
  ]),
)
const categoryModeItems = computed(() => [
  { label: t('batchMetadata.keep'), value: 'keep' },
  { label: t('batchMetadata.set'), value: 'set' },
  { label: t('batchMetadata.clear'), value: 'clear' },
])
const tagModeItems = computed(() => [
  { label: t('batchMetadata.keep'), value: 'keep' },
  { label: t('batchMetadata.add'), value: 'add' },
  { label: t('batchMetadata.remove'), value: 'remove' },
  { label: t('batchMetadata.replace'), value: 'replace' },
  { label: t('batchMetadata.clear'), value: 'clear' },
])
const tagModeNeedsValues = computed(() => ['add', 'remove', 'replace'].includes(batchDraft.tagMode))
const batchDraftValid = computed(() => hasBatchMetadataChange(batchDraft))
const categoryModeHint = computed(() =>
  t(
    batchDraft.categoryMode === 'clear'
      ? 'batchMetadata.category_clear_hint'
      : 'batchMetadata.keep_hint',
  ),
)
const tagModeHint = computed(() =>
  t(batchDraft.tagMode === 'clear' ? 'batchMetadata.tags_clear_hint' : 'batchMetadata.keep_hint'),
)
const templateItems = computed(() => [
  { label: t('workflow.list.template_generic'), value: 'generic' },
  { label: t('workflow.list.template_windows'), value: 'windows' },
  { label: t('workflow.list.template_android'), value: 'android' },
  { label: t('workflow.list.template_browser'), value: 'browser' },
  { label: t('workflow.list.template_cross_target'), value: 'cross-target' },
])
const columnOptions = computed<Array<{ key: WorkflowColumn; label: string }>>(() => [
  { key: 'category', label: t('workflow.list.category') },
  { key: 'tags', label: t('workflow.list.tags') },
  { key: 'nodes', label: t('workflow.list.nodes') },
  { key: 'revision', label: t('workflow.list.revision') },
  { key: 'createdAt', label: t('workflow.list.created_at') },
  { key: 'updatedAt', label: t('workflow.list.updated_at') },
])
const visibleColumnSet = computed(() => new Set(visibleColumns.value))
const columnMenuItems = computed(() => [
  columnOptions.value.map((column) => ({
    label: column.label,
    type: 'checkbox' as const,
    checked: visibleColumnSet.value.has(column.key),
    onUpdateChecked: (checked: boolean) => setColumnVisible(column.key, checked),
  })),
  [
    {
      label: t('workflow.list.reset_columns'),
      icon: 'i-tabler-restore',
      onSelect: () => {
        visibleColumns.value = [...defaultColumns]
      },
    },
  ],
])
const workflowGridTemplate = computed(() => {
  const columns = ['2rem', 'minmax(18rem, 2fr)']
  if (isColumnVisible('category')) columns.push('minmax(8rem, 0.8fr)')
  if (isColumnVisible('tags')) columns.push('minmax(12rem, 1.2fr)')
  if (isColumnVisible('nodes')) columns.push('5rem')
  if (isColumnVisible('revision')) columns.push('5rem')
  if (isColumnVisible('createdAt')) columns.push('8.5rem')
  if (isColumnVisible('updatedAt')) columns.push('8.5rem')
  columns.push('8.5rem')
  return columns.join(' ')
})
const libraryMenuItems = computed(() => [
  [
    {
      label: t('workflow.list.import_source'),
      icon: 'i-tabler-file-import',
      disabled: portabilityBusy.value,
      onSelect: () => void importSourceBundle(),
    },
    { label: t('common.refresh'), icon: 'i-tabler-refresh', onSelect: () => void load() },
  ],
])

watch(
  visibleColumns,
  (value) => localStorage.setItem('yotta.workflow.columns', JSON.stringify(value)),
  { deep: true },
)
const unsubscribeRun = onRunChanged((event) => {
  const entry = Object.entries(activeRunIdByWorkflow).find(([, runId]) => runId === event.runId)
  if (!entry) return
  settleWorkflowRun(entry[0], event.runId, event.status)
})
onMounted(load)
onBeforeUnmount(unsubscribeRun)

async function load(): Promise<void> {
  loading.value = true
  failure.value = ''
  try {
    const [result, isolated] = await Promise.all([
      workflowTransport.querySources({
        ...libraryQuery.request(),
      }),
      workflowTransport.listSourceRecoveries(),
    ])
    sources.value = result.items
    recoveries.value = isolated
    if (libraryQuery.accept(result)) await load()
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    loading.value = false
  }
}

function openWorkflow(workflowId: string): void {
  void router.push(`/workflows/${workflowId}/edit`)
}

function openBatchEdit(): void {
  Object.assign(batchDraft, createBatchMetadataDraft())
  batchEditing.value = true
}

async function saveBatchMetadata(): Promise<void> {
  if (!selectedRows.value.length || !batchDraftValid.value || batchBusy.value) return
  batchBusy.value = true
  portabilityFeedback.value = null
  try {
    const results = await workflowTransport.batchUpdateSourceMetadata(
      selectedRows.value.map((source) => {
        const metadata = applyBatchMetadata(
          { category: source.category ?? '', tags: source.tags ?? [] },
          batchDraft,
        )
        return {
          workflowId: source.workflowId,
          baseRevision: source.revision,
          category: metadata.category,
          tags: metadata.tags,
        }
      }),
    )
    const failed = results.filter((result) => !result.updated)
    retainFailedWorkflowSelection(failed.map((result) => result.workflowId))
    portabilityFeedback.value = {
      tone: failed.length ? 'warning' : 'success',
      message: t('workflow.list.batch_update_result', {
        updated: results.length - failed.length,
        failed: failed.length,
      }),
      details: failed.map(
        (result) => `${selectedName(result.workflowId)}: ${errorMessage(result.problem)}`,
      ),
    }
    batchEditing.value = false
    await load()
  } catch (error) {
    portabilityFeedback.value = { tone: 'error', message: errorText(error), details: [] }
  } finally {
    batchBusy.value = false
  }
}

function isColumnVisible(key: WorkflowColumn): boolean {
  return visibleColumnSet.value.has(key)
}

function setColumnVisible(key: WorkflowColumn, checked: boolean): void {
  const current = new Set(visibleColumns.value)
  if (checked) current.add(key)
  else current.delete(key)
  visibleColumns.value = columnOptions.value
    .map((item) => item.key)
    .filter((item) => current.has(item))
}

function loadColumns(): WorkflowColumn[] {
  try {
    const raw = JSON.parse(localStorage.getItem('yotta.workflow.columns') ?? '[]') as unknown
    if (!Array.isArray(raw)) return [...defaultColumns]
    const allowed = new Set<WorkflowColumn>([
      'category',
      'tags',
      'nodes',
      'revision',
      'createdAt',
      'updatedAt',
    ])
    const values = raw.filter(
      (value): value is WorkflowColumn =>
        typeof value === 'string' && allowed.has(value as WorkflowColumn),
    )
    return values.length ? values : [...defaultColumns]
  } catch {
    return [...defaultColumns]
  }
}

function openCreateModal(): void {
  editingSource.value = null
  metadataDraft.name = ''
  metadataDraft.description = ''
  metadataDraft.category = ''
  metadataDraft.tags = []
  metadataDraft.template = 'generic'
  metadataFailure.value = ''
  metadataModalOpen.value = true
}

function openMetadataEditor(source: SourceView): void {
  editingSource.value = source
  metadataDraft.name = source.name
  metadataDraft.description = source.description ?? ''
  metadataDraft.category = source.category ?? ''
  metadataDraft.tags = [...(source.tags ?? [])]
  metadataDraft.template = 'generic'
  metadataFailure.value = ''
  metadataModalOpen.value = true
}

async function saveMetadata(): Promise<void> {
  if (!metadataDraft.name.trim() || metadataBusy.value) return
  metadataBusy.value = true
  metadataFailure.value = ''
  try {
    const request = {
      name: metadataDraft.name.trim(),
      description: metadataDraft.description.trim(),
      category: metadataDraft.category.trim(),
      tags: uniqueStrings(metadataDraft.tags),
    }
    const editing = editingSource.value
    if (editing) {
      await workflowTransport.updateSourceMetadata(editing.workflowId, editing.revision, request)
      metadataModalOpen.value = false
      await load()
      return
    }
    const created = await workflowTransport.createSourceWithMetadata(request)
    metadataModalOpen.value = false
    const template = metadataDraft.template
    await router.push({
      path: `/workflows/${created.workflowId}/edit`,
      query: template === 'generic' ? {} : { template },
    })
  } catch (error) {
    metadataFailure.value = errorText(error)
  } finally {
    metadataBusy.value = false
  }
}

function createMetadataCategory(value: string): void {
  const category = value.trim()
  if (!category) return
  createdCategories.value = uniqueStrings([...createdCategories.value, category])
  metadataDraft.category = category
}

function createMetadataTag(value: string): void {
  const tag = value.trim()
  if (!tag) return
  createdTags.value = uniqueStrings([...createdTags.value, tag])
  metadataDraft.tags = uniqueStrings([...metadataDraft.tags, tag])
}

function createBatchCategory(value: string): void {
  const category = value.trim()
  if (!category) return
  createdCategories.value = uniqueStrings([...createdCategories.value, category])
  batchDraft.category = category
}

function createBatchTag(value: string): void {
  const tag = value.trim()
  if (!tag) return
  createdTags.value = uniqueStrings([...createdTags.value, tag])
  batchDraft.tags = uniqueMetadataValues([...batchDraft.tags, tag])
}

function formatListDate(value?: string): string {
  if (!value) return '—'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return '—'
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium' }).format(parsed)
}

function formatExactDate(value: string): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return new Intl.DateTimeFormat(locale.value, {
    dateStyle: 'long',
    timeStyle: 'medium',
  }).format(parsed)
}

function uniqueStrings(values: string[]): string[] {
  const seen = new Set<string>()
  return values
    .map((value) => value.trim())
    .filter((value) => {
      const key = value.toLocaleLowerCase()
      if (!key || seen.has(key)) return false
      seen.add(key)
      return true
    })
}

function rowMenuItems(source: SourceView) {
  return [
    [
      {
        label: t('workflow.list.edit_metadata'),
        icon: 'i-tabler-edit',
        onSelect: () => openMetadataEditor(source),
      },
      {
        label: t('workflow.market.publish_action'),
        icon: 'i-tabler-cloud-upload',
        onSelect: () => openPublish(source),
      },
      {
        label: t('workflow.market.clone'),
        icon: 'i-tabler-copy',
        onSelect: () => void cloneWorkflow(source.workflowId),
      },
      {
        label: t('workflow.list.export_source'),
        icon: 'i-tabler-file-export',
        disabled: portabilityBusy.value,
        onSelect: () => void exportSource(source),
      },
      {
        label: t('workflow.list.replace_source'),
        icon: 'i-tabler-file-arrow-left',
        disabled: portabilityBusy.value,
        onSelect: () => void replaceSource(source),
      },
    ],
    [
      {
        label: t('common.delete'),
        icon: 'i-tabler-trash',
        color: 'error' as const,
        onSelect: () => void requestDelete([source]),
      },
    ],
  ]
}

function openPublish(source: SourceView): void {
  if (publishAuthBusy.value) return
  pendingPublishSource.value = source
  void publicationEntry.start(() => initializePublish(source))
}

function initializePublish(source: SourceView): void {
  publishResult.value = ''
  latestSubmission.value = null
  Object.assign(salesDraft.value, {
    paid: false,
    price: '',
    revision: 0,
    available: true,
    configured: false,
  })
  publishBundle.value = null
  publishCategoryDirectory.value = []
  publishFilterDimensions.value = []
  publishNeedsLogin.value = false
  publishHistoryFailed.value = false
  publishLoading.value = true
  const generation = ++publishGeneration
  publishTouched.clear()
  publishSource.value = source
  publishDraft.releaseVersion = '1.0.0'
  publishHighestVersion.value = ''
  publishDraft.title = source.name
  publishDraft.summary = source.description?.trim() || source.name
  publishDraft.releaseNotes = ''
  publishDraft.listing = {
    filterValues: [],
    icon: 'i-tabler-route',
    category: '',
    tags: [...(source.tags || [])],
    description: '',
    instructions: '',
  }
  publishSection.value = 'listing'
  publishFailure.value = ''
  publishOpen.value = true
  void Promise.all([
    shopTransport.discover({
      workflowIds: [source.workflowId],
      search: '',
      category: '',
      tag: '',
      sort: 'updated',
      cursor: '',
      limit: 1,
    }),
    workflowTransport.previewSourceBundle(source.workflowId),
    shopTransport.categories(),
    shopTransport.filterCatalog(),
    shopTransport.publicationHistory(source.workflowId),
  ])
    .then(([current, bundle, categories, filterCatalog, history]) => {
      if (generation !== publishGeneration || !publishOpen.value) return
      publishBundle.value = bundle
      publishCategoryDirectory.value = categories
      publishFilterDimensions.value = filterCatalog.dimensions
      latestSubmission.value = history.items[0] || null
      const rejected = history.items[0]?.status === 'rejected' ? history.items[0] : undefined
      const sales = rejected?.sales || history.sales || history.items[0]?.sales
      if (sales)
        Object.assign(salesDraft.value, {
          paid: sales.priceCents > 0,
          price: (sales.priceCents / 100).toFixed(2),
          revision: history.sales?.revision || 0,
          available: sales.available,
          configured: !!history.sales,
        })
      const previous = rejected?.release || current.items[0] || history.items[0]?.release
      if (!previous || generation !== publishGeneration || !publishOpen.value || publishing.value)
        return
      publishHighestVersion.value = current.items[0]?.releaseVersion || ''
      if (!publishTouched.has('version'))
        publishDraft.releaseVersion = publicationVersion(
          publishHighestVersion.value,
          rejected?.release.releaseVersion,
        )
      if (!publishTouched.has('title')) publishDraft.title = previous.title
      if (!publishTouched.has('summary')) publishDraft.summary = previous.summary
      const listing = {
        filterValues: [...(previous.listing?.filterValues || [])],
        icon: previous.listing?.icon || 'i-tabler-route',
        category: previous.listing?.category || '',
        tags: [...(previous.listing?.tags || source.tags || [])],
        description: previous.listing?.description || '',
        instructions: previous.listing?.instructions || '',
      }
      for (const key of ['icon', 'category', 'description', 'instructions'] as const)
        if (!publishTouched.has(key)) publishDraft.listing[key] = listing[key]
      if (!publishTouched.has('tags')) publishDraft.listing.tags = listing.tags
      if (!publishTouched.has('filterValues'))
        publishDraft.listing.filterValues = listing.filterValues
    })
    .catch((error) => {
      if (generation === publishGeneration) {
        publishHistoryFailed.value = true
        publishFailure.value = errorMessage(error)
      }
    })
    .finally(() => {
      if (generation === publishGeneration) publishLoading.value = false
    })
}

async function publishWorkflow(): Promise<void> {
  const source = publishSource.value
  if (
    !source ||
    publishNeedsLogin.value ||
    publishing.value ||
    !!publishResult.value ||
    latestSubmission.value?.status === 'pending_review' ||
    !validSalesDraft(salesDraft.value) ||
    publishLoading.value ||
    publishHistoryFailed.value ||
    !publishVersionValid.value ||
    !publishCategoryValid.value ||
    !publishMarkdownValid.value
  )
    return
  publishing.value = true
  publishFailure.value = ''
  try {
    const result = await workflowTransport.publishSourceToRegistry({
      resubmissionId:
        latestSubmission.value?.status === 'rejected'
          ? latestSubmission.value.submissionId
          : undefined,
      sales: publicationSales(salesDraft.value),
      listing: publishDraft.listing,
      workflowId: source.workflowId,
      releaseVersion: publishDraft.releaseVersion.trim(),
      title: publishDraft.title.trim(),
      summary: publishDraft.summary.trim(),
      releaseNotes: publishDraft.releaseNotes.trim(),
      examples: [],
    })
    publishResult.value = result.publicationStatus || 'published'
  } catch (error) {
    publishFailure.value = errorMessage(error)
    publishNeedsLogin.value =
      normalizeError(error).id === 'workflow.registry.authentication_required'
  } finally {
    publishing.value = false
  }
}

async function openPublicationHistory(): Promise<void> {
  try {
    await shopTransport.openMySubmissions()
  } catch (error) {
    publishFailure.value = errorMessage(error)
  }
}

async function recoverPublishLogin(): Promise<void> {
  if (publishing.value) return
  publishing.value = true
  try {
    await shopTransport.logout()
    await shopTransport.login()
    publishNeedsLogin.value = false
    publishFailure.value = ''
  } catch (error) {
    publishFailure.value = errorMessage(error)
  } finally {
    publishing.value = false
  }
}

async function cancelPublish(): Promise<void> {
  try {
    if (publishing.value) await shopTransport.cancelLogin()
    else publishOpen.value = false
  } catch (error) {
    publishFailure.value = errorMessage(error)
  }
}

async function cloneWorkflow(workflowId: string): Promise<void> {
  try {
    const source = await shopTransport.clone(workflowId)
    await load()
    openWorkflow(source.workflowId)
  } catch (error) {
    failure.value = errorMessage(error)
  }
}

function openRecoveryRepair(recovery: SourceRecoveryView): void {
  activeRecovery.value = recovery
  recoveryDraft.value = recovery.sourceJson
  recoveryFailure.value = ''
  recoveryRepairOpen.value = true
}

async function repairRecovery(): Promise<void> {
  const recovery = activeRecovery.value
  if (!recovery || recoveryBusyId.value || !recoveryDraft.value.trim()) return
  recoveryBusyId.value = recovery.recoveryId
  recoveryFailure.value = ''
  try {
    await workflowTransport.repairSourceRecovery(recovery.recoveryId, recoveryDraft.value)
    recoveryRepairOpen.value = false
    activeRecovery.value = null
    recoveryDraft.value = ''
    await load()
  } catch (error) {
    recoveryFailure.value = errorText(error)
  } finally {
    recoveryBusyId.value = ''
  }
}

async function deleteRecovery(recovery: SourceRecoveryView): Promise<void> {
  if (recoveryBusyId.value) return
  const accepted = await confirm({
    title: t('workflow.list.recovery_delete_title', { name: recovery.originalName }),
    description: t('workflow.list.recovery_delete_description'),
    confirmText: t('common.delete'),
    cancelText: t('common.cancel'),
    color: 'error',
  })
  if (accepted !== true) return
  recoveryBusyId.value = recovery.recoveryId
  try {
    await workflowTransport.deleteSourceRecovery(recovery.recoveryId)
    await load()
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    recoveryBusyId.value = ''
  }
}

async function importSourceBundle(): Promise<void> {
  if (portabilityBusy.value) return
  portabilityFeedback.value = null
  importing.value = true
  try {
    const path = await workflowTransport.chooseSourceBundle()
    if (!path) return
    const info = await workflowTransport.inspectSourceBundle(path)
    const accepted = await confirm({
      title: t('workflow.list.import_title', { name: info.name }),
      description: bundleDescription(info),
      confirmText: t('workflow.list.import_source'),
      cancelText: t('common.cancel'),
    })
    if (accepted !== true) return
    await workflowTransport.importSourceBundle(path)
    await load()
  } catch (error) {
    portabilityFeedback.value = { tone: 'error', message: errorText(error), details: [] }
  } finally {
    importing.value = false
  }
}

async function exportSource(source: SelectedSource): Promise<void> {
  if (portabilityBusy.value) return
  portabilityFeedback.value = null
  exportingId.value = source.workflowId
  try {
    const destination = await workflowTransport.chooseSourceBundleDestination(
      `${source.workflowId}.yotta-workflow`,
    )
    if (!destination) return
    const result = await workflowTransport.exportSourceBundle(source.workflowId, destination)
    portabilityFeedback.value = {
      tone: 'success',
      message: t('workflow.list.export_result', { n: 1 }),
      details: result.path ? [result.path] : [],
    }
  } catch (error) {
    portabilityFeedback.value = { tone: 'error', message: errorText(error), details: [] }
  } finally {
    exportingId.value = ''
  }
}

async function exportSelected(): Promise<void> {
  if (!selectedRows.value.length || portabilityBusy.value) return
  portabilityFeedback.value = null
  batchExporting.value = true
  try {
    const directory = await workflowTransport.chooseSourceBundleDirectory()
    if (!directory) return
    const results = await workflowTransport.exportSourceBundles(
      selectedRows.value.map((source) => source.workflowId),
      directory,
    )
    const failed = results.filter((result) => !result.exported)
    const exported = results.filter((result) => result.exported)
    portabilityFeedback.value = {
      tone: failed.length ? 'warning' : 'success',
      message: t('workflow.list.export_batch_result', {
        exported: exported.length,
        failed: failed.length,
      }),
      details: failed.map(
        (result) => `${selectedName(result.workflowId)}: ${errorMessage(result.problem)}`,
      ),
    }
  } catch (error) {
    portabilityFeedback.value = { tone: 'error', message: errorText(error), details: [] }
  } finally {
    batchExporting.value = false
  }
}

async function replaceSource(source: SelectedSource): Promise<void> {
  if (portabilityBusy.value) return
  portabilityFeedback.value = null
  replacingId.value = source.workflowId
  try {
    const path = await workflowTransport.chooseSourceBundle()
    if (!path) return
    const info = await workflowTransport.inspectSourceBundle(path)
    const accepted = await confirm({
      title: t('workflow.list.replace_title', { name: source.name }),
      description: `${bundleDescription(info)}\n${t('workflow.list.replace_description')}`,
      confirmText: t('workflow.list.replace_source'),
      cancelText: t('common.cancel'),
      color: 'warning',
    })
    if (accepted !== true) return
    await workflowTransport.replaceSourceFromBundle(
      path,
      source.workflowId,
      source.revision,
      source.sourceHash,
    )
    await load()
  } catch (error) {
    portabilityFeedback.value = { tone: 'error', message: errorText(error), details: [] }
  } finally {
    replacingId.value = ''
  }
}

function bundleDescription(info: BundleInfoView): string {
  const description = t('workflow.list.bundle_description', {
    name: info.name,
    revision: info.revision,
    blobs: info.blobCount,
    bytes: info.blobBytes,
  })
  return info.panels?.length
    ? `${description}\n${t('panels.bundle_import', { count: info.panels.length })}\n${info.panels.map((panel) => panel.title).join('、')}`
    : description
}

async function requestDelete(rows: SelectedSource[]): Promise<void> {
  if (!rows.length || deleting.value) return
  deleting.value = true
  deleteFeedback.value = null
  try {
    const previews = await workflowTransport.previewDeleteSources(rows.map((row) => row.workflowId))
    const blocked = previews.filter((preview) => preview.references.length > 0)
    const deletableIds = new Set(
      previews
        .filter((preview) => preview.references.length === 0)
        .map((preview) => preview.workflowId),
    )
    const details = blocked.flatMap(referenceDetails)
    if (deletableIds.size === 0) {
      deleteFeedback.value = {
        tone: 'warning',
        message: t('workflow.list.delete_all_blocked'),
        details,
      }
      return
    }
    const accepted = await confirm({
      title: t('workflow.list.delete_title', { n: deletableIds.size }),
      description: [
        t('workflow.list.delete_description', {
          deletable: deletableIds.size,
          blocked: blocked.length,
        }),
        ...details,
      ].join('\n'),
      confirmText: t('common.delete'),
      cancelText: t('common.cancel'),
      color: 'error',
    })
    if (accepted !== true) return
    const requests = rows
      .filter((row) => deletableIds.has(row.workflowId))
      .map((row) => ({
        workflowId: row.workflowId,
        revision: row.revision,
        sourceHash: row.sourceHash,
      }))
    const results = await workflowTransport.deleteSources(requests)
    const deleted = results.filter((result) => result.deleted)
    const failed = results.filter((result) => !result.deleted)
    for (const result of deleted) {
      delete runFeedbackById[result.workflowId]
    }
    removeWorkflowSelection(deleted.map((result) => result.workflowId))
    deleteFeedback.value =
      failed.length || blocked.length
        ? {
            tone: 'warning',
            message: t('workflow.list.delete_result', {
              deleted: deleted.length,
              failed: failed.length,
              blocked: blocked.length,
            }),
            details: [
              ...details,
              ...failed.map(
                (result) =>
                  `${sourceName(result.workflowId, previews)}: ${errorMessage(result.problem)}`,
              ),
            ],
          }
        : null
    await load()
  } catch (error) {
    deleteFeedback.value = { tone: 'error', message: errorText(error), details: [] }
  } finally {
    deleting.value = false
  }
}

function referenceDetails(preview: DeleteSourcePreview): string[] {
  return preview.references.map(
    (reference) =>
      `${preview.name}: ${t(`workflow.list.reference_${reference.kind}`, { name: reference.label || reference.id })}`,
  )
}

function sourceName(workflowId: string, previews: DeleteSourcePreview[]): string {
  return previews.find((preview) => preview.workflowId === workflowId)?.name ?? workflowId
}

async function runWorkflow(workflowId: string): Promise<void> {
  if (runStartingId.value) return
  runStartingId.value = workflowId
  try {
    const started = await workflowTransport.startRun(workflowId)
    const outcome = runStartOutcome(started)
    if (outcome.state !== 'started') {
      runFeedbackById[workflowId] = {
        tone: 'warning',
        label: t('workflow.toast.not_started'),
        detail: runReadinessMessage(outcome),
      }
      return
    }
    activeRunIdByWorkflow[workflowId] = outcome.runId
    runFeedbackById[workflowId] = {
      tone: 'warning',
      label: t('workflow.status.running'),
      detail: outcome.runId,
    }
    const initialStatus = started.run?.status ?? ''
    if (isTerminalRunStatus(initialStatus)) {
      settleWorkflowRun(workflowId, outcome.runId, initialStatus)
      return
    }
    const terminal = await pollTerminalRunStatus(
      async () => (await workflowTransport.getRunTimeline(outcome.runId)).status,
      () => activeRunIdByWorkflow[workflowId] !== outcome.runId,
    )
    if (terminal) settleWorkflowRun(workflowId, outcome.runId, terminal)
  } catch (error) {
    delete activeRunIdByWorkflow[workflowId]
    delete runFeedbackById[workflowId]
    toast.add({
      title: t('workflow.toast.run_failed'),
      description: errorText(error),
      color: 'error',
    })
  } finally {
    runStartingId.value = ''
  }
}

async function stopWorkflow(workflowId: string): Promise<void> {
  const runId = activeRunIdByWorkflow[workflowId]
  if (!runId || runStartingId.value) return
  runStartingId.value = workflowId
  try {
    const stopped = await workflowTransport.cancelRun(runId)
    settleWorkflowRun(workflowId, runId, stopped.status || 'cancelled')
  } catch (error) {
    toast.add({
      title: t('workflow.toast.stop_failed'),
      description: errorText(error),
      color: 'error',
    })
  } finally {
    runStartingId.value = ''
  }
}

function settleWorkflowRun(workflowId: string, runId: string, rawStatus: string): void {
  if (activeRunIdByWorkflow[workflowId] !== runId) return
  const status = rawStatus.toLowerCase()
  if (status === 'queued' || status === 'running') {
    runFeedbackById[workflowId] = {
      tone: 'warning',
      label: t(`workflow.status.${status}`),
      detail: runId,
    }
    return
  }
  delete activeRunIdByWorkflow[workflowId]
  const feedback =
    status === 'succeeded'
      ? { tone: 'success' as const, label: t('workflow.toast.run_completed') }
      : status === 'cancelled' || status === 'interrupted'
        ? { tone: 'neutral' as const, label: t('workflow.toast.run_cancelled') }
        : { tone: 'error' as const, label: t('workflow.toast.run_failed') }
  runFeedbackById[workflowId] = { ...feedback, detail: runId }
  window.setTimeout(() => {
    if (!activeRunIdByWorkflow[workflowId] && runFeedbackById[workflowId]?.detail === runId)
      delete runFeedbackById[workflowId]
  }, 3000)
}

function isTerminalRunStatus(status: string): boolean {
  return ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(status.toLowerCase())
}

function errorText(error: unknown): string {
  return errorMessage(error)
}
</script>
