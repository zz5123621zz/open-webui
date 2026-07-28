<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import type { GuidanceSubmission, TaskBriefData } from './types';
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';
  import ProfileUpdateProposal from './ProfileUpdateProposal.svelte';

  export let data: TaskBriefData;
  export let sourceMessageId: string;
  export let sourcePartId: string;
  export let conversationId: string;
  export let userId: string;
  export let locale: Locale = 'zh-CN';
  export let current = false;
  export let disabled = false;
  export let draftEnabled = true;

  const dispatch = createEventDispatcher<{
    submit: { submission: GuidanceSubmission };
  }>();

  let addingContext = false;
  let additionalText = '';
  let profileDecision: 'save' | 'current_task_only' | 'ignore' =
    'current_task_only';
  let localError = '';
  let initializedKey = '';
  let interactionDisabled = true;

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: draftKey =
    `restaurant-guidance-draft-v1:${userId}:${conversationId}:${sourceMessageId}`;
  $: if (draftKey !== initializedKey) initializeDraft(draftKey);
  $: if (!current && initializedKey === draftKey) clearDraft();
  $: interactionDisabled = disabled || !current;

  function initializeDraft(key: string) {
    initializedKey = key;
    addingContext = false;
    additionalText = '';
    profileDecision = 'current_task_only';
    if (draftEnabled) {
      try {
        const raw = localStorage.getItem(key);
        const saved = raw
          ? (JSON.parse(raw) as {
              addingContext?: boolean;
              additionalText?: string;
              profileDecision?: string;
            })
          : {};
        addingContext = saved.addingContext === true;
        additionalText =
          typeof saved.additionalText === 'string'
            ? Array.from(saved.additionalText).slice(0, 600).join('')
            : '';
        if (
          saved.profileDecision === 'save' ||
          saved.profileDecision === 'current_task_only' ||
          saved.profileDecision === 'ignore'
        ) {
          profileDecision = saved.profileDecision;
        }
      } catch {
        localStorage.removeItem(key);
      }
    }
    localError = '';
  }

  function persistDraft() {
    if (!draftEnabled || !current) return;
    localStorage.setItem(
      draftKey,
      JSON.stringify({ addingContext, additionalText, profileDecision })
    );
  }

  function clearDraft() {
    if (draftEnabled) localStorage.removeItem(draftKey);
  }

  function profileFields(): Pick<GuidanceSubmission, 'profileDecision'> {
    return data.profileUpdateProposal ? { profileDecision } : {};
  }

  function confirmBrief() {
    if (interactionDisabled) return;
    dispatch('submit', {
      submission: {
        sourceAssistantMessageId: sourceMessageId,
        sourcePartId,
        intent: 'confirm_brief',
        answers: [],
        ...profileFields()
      }
    });
  }

  function openAdditionalContext() {
    if (interactionDisabled) return;
    addingContext = true;
    localError = '';
    persistDraft();
  }

  function updateAdditionalText(event: Event) {
    additionalText = (event.currentTarget as HTMLTextAreaElement).value;
    localError = '';
    persistDraft();
  }

  function submitAdditionalContext() {
    if (interactionDisabled || !additionalText.trim()) {
      localError = t('请先写下要补充的内容。', 'Add the context you want to provide.');
      return;
    }
    dispatch('submit', {
      submission: {
        sourceAssistantMessageId: sourceMessageId,
        sourcePartId,
        intent: 'add_context',
        answers: [],
        additionalText: additionalText.trim(),
        ...profileFields()
      }
    });
  }

  function sectionLabel(key: string): [string, string] {
    const labels: Record<string, [string, string]> = {
      context: ['相关背景', 'Relevant context'],
      constraints: ['已确认约束', 'Confirmed constraints'],
      desiredOutput: ['希望得到', 'Desired output'],
      delegatedAssumptions: ['由我代为采用的默认值', 'Delegated assumptions'],
      unresolved: ['仍未确认', 'Still unresolved']
    };
    return labels[key];
  }
</script>

<section
  class:readonly={!current}
  class="task-brief"
  aria-label={t('任务简报', 'Task brief')}
>
  <header>
    <span aria-hidden="true"><Icon name="plan" size={16} /></span>
    <div>
      <strong>{t('任务简报', 'Task brief')}</strong>
      <small>{t('确认后再生成完整方案', 'Confirm before generating the full answer')}</small>
    </div>
  </header>
  <div class="brief-body">
    <section class="goal">
      <b>{t('本次目标', 'Goal')}</b>
      <p>{data.goal}</p>
    </section>
    {#each [
      ['context', data.context],
      ['constraints', data.constraints],
      ['desiredOutput', data.desiredOutput],
      ['delegatedAssumptions', data.delegatedAssumptions],
      ['unresolved', data.unresolved]
    ] as section}
      {@const key = String(section[0])}
      {@const values = section[1] as string[]}
      {#if values.length}
        <section class="brief-section">
          <b>{t(...sectionLabel(key))}</b>
          <ul>{#each values as item}<li>{item}</li>{/each}</ul>
        </section>
      {/if}
    {/each}

    {#if data.profileUpdateProposal}
      <ProfileUpdateProposal
        proposal={data.profileUpdateProposal}
        {locale}
        value={profileDecision}
        disabled={interactionDisabled}
        on:change={(event) => {
          profileDecision = event.detail.value;
          persistDraft();
        }}
      />
    {/if}

    {#if addingContext && current}
      <label class="additional-context">
        <span>{t('补充说明', 'Additional context')}</span>
        <textarea
          rows="3"
          maxlength="600"
          value={additionalText}
          disabled={interactionDisabled}
          on:input={updateAdditionalText}
        ></textarea>
      </label>
      <div class="additional-actions">
        <button
          type="button"
          class="secondary-action"
          disabled={interactionDisabled}
          on:click={() => {
            addingContext = false;
            persistDraft();
          }}
        >
          {t('取消', 'Cancel')}
        </button>
        <button
          type="button"
          class="primary-action"
          disabled={interactionDisabled || !additionalText.trim()}
          on:click={submitAdditionalContext}
        >
          <Icon name="send" size={15} />
          {t('提交补充', 'Submit context')}
        </button>
      </div>
    {/if}
    {#if localError}<p class="card-error" role="alert">{localError}</p>{/if}
  </div>

  {#if current && !addingContext}
    <footer>
      <button
        type="button"
        class="secondary-action"
        disabled={interactionDisabled}
        on:click={openAdditionalContext}
      >
        {t('我再补充', 'Add more context')}
      </button>
      <button
        type="button"
        class="primary-action"
        disabled={interactionDisabled}
        on:click={confirmBrief}
      >
        <Icon name="send" size={15} />
        {t('按当前需求生成', 'Generate from this brief')}
      </button>
    </footer>
  {:else if !current}
    <p class="readonly-note">
      <Icon name="check" size={14} />
      {t('这份简报已确认或已被后续消息替代', 'This brief was confirmed or superseded')}
    </p>
  {/if}
</section>

<style>
  .task-brief {
    width: min(100%, 760px);
    margin: 8px 0 14px;
    overflow: hidden;
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 16px;
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .task-brief > header {
    display: flex;
    gap: 10px;
    align-items: center;
    padding: 15px 16px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-soft);
  }

  header > span {
    display: grid;
    width: 32px;
    height: 32px;
    flex: 0 0 32px;
    place-items: center;
    color: var(--primary);
    border-radius: 10px;
    background: var(--primary-soft);
  }

  header strong,
  header small {
    display: block;
  }

  header small {
    margin-top: 2px;
    color: var(--text-soft);
    font-size: 0.8rem;
  }

  .brief-body {
    padding: 16px;
    overflow-wrap: anywhere;
  }

  .goal {
    padding: 12px 14px;
    border-left: 3px solid var(--primary);
    border-radius: 0 10px 10px 0;
    background: var(--primary-soft);
  }

  .goal b,
  .brief-section b {
    color: var(--text-soft);
    font-size: 0.8rem;
  }

  .goal p {
    margin: 4px 0 0;
    font-weight: 650;
  }

  .brief-section {
    padding: 14px 0;
    border-bottom: 1px solid var(--border);
  }

  .brief-section ul {
    margin: 6px 0 0;
    padding-left: 20px;
  }

  .brief-section li + li {
    margin-top: 4px;
  }

  .additional-context {
    display: grid;
    gap: 7px;
    margin-top: 14px;
    color: var(--text-soft);
    font-size: 0.86rem;
    font-weight: 650;
  }

  .additional-context textarea {
    width: 100%;
    min-height: 96px;
    resize: vertical;
    padding: 10px 12px;
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface);
    font-size: 1rem;
  }

  .additional-context textarea:focus {
    border-color: var(--primary);
  }

  .additional-actions,
  footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }

  .additional-actions {
    margin-top: 10px;
  }

  footer {
    padding: 14px 16px 16px;
    border-top: 1px solid var(--border);
    background: var(--surface-soft);
  }

  .additional-actions button,
  footer button {
    min-height: 44px;
    padding: 9px 14px;
    border-radius: 10px;
    font-weight: 650;
    cursor: pointer;
  }

  .secondary-action {
    color: var(--text);
    border: 1px solid var(--border-strong);
    background: var(--surface);
  }

  .primary-action {
    display: inline-flex;
    gap: 7px;
    align-items: center;
    color: var(--primary-contrast);
    border: 1px solid var(--primary);
    background: var(--primary);
  }

  button:disabled {
    cursor: not-allowed;
    opacity: 0.5;
  }

  .card-error {
    margin: 10px 0 0;
    color: var(--danger);
    font-size: 0.86rem;
  }

  .readonly-note {
    display: flex;
    gap: 7px;
    align-items: center;
    margin: 0;
    padding: 12px 16px;
    color: var(--text-muted);
    border-top: 1px solid var(--border);
    background: var(--surface-soft);
    font-size: 0.84rem;
  }

  .readonly {
    box-shadow: none;
  }

  @media (max-width: 620px) {
    footer {
      flex-direction: column-reverse;
    }

    footer button {
      width: 100%;
    }
  }
</style>
