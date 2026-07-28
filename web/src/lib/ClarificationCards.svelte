<script lang="ts">
  import { createEventDispatcher, tick } from 'svelte';
  import type {
    ClarificationAnswer,
    ClarificationCardsData,
    ClarificationQuestion,
    GuidanceSubmission
  } from './types';
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';

  type DraftAnswer = {
    selectedOptionKeys: string[];
    otherText: string;
    delegatedDefault: boolean;
    otherOpen: boolean;
  };

  export let data: ClarificationCardsData;
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

  let answers: Record<string, DraftAnswer> = {};
  let localError = '';
  let initializedKey = '';
  let previewRows: Array<{ prompt: string; answer: string }> = [];
  let previewUnresolved: string[] = [];
  let interactionDisabled = true;
  let complete = false;
  let currentRoundNumber: number | null = null;
  let roundLimit: number | null = null;
  let finalRound = false;

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: currentRoundNumber =
    typeof data.round === 'number' && Number.isInteger(data.round) && data.round > 0
      ? data.round
      : null;
  $: roundLimit =
    typeof data.maxRounds === 'number' &&
    Number.isInteger(data.maxRounds) &&
    data.maxRounds > 0
      ? data.maxRounds
      : null;
  $: finalRound =
    currentRoundNumber !== null &&
    roundLimit !== null &&
    currentRoundNumber >= roundLimit;
  $: draftKey =
    `restaurant-guidance-draft-v1:${userId}:${conversationId}:${sourceMessageId}`;
  $: if (draftKey !== initializedKey) initializeDraft(draftKey);
  $: if (!current && initializedKey === draftKey) clearDraft();
  $: interactionDisabled = disabled || !current;
  $: {
    answers;
    complete = data.questions.every((question) => answerValid(question));
  }
  $: {
    answers;
    locale;
    previewRows = data.questions
      .filter((question) => answerValid(question))
      .map((question) => ({
        prompt: question.prompt,
        answer: answerSummary(question)
      }));
    previewUnresolved = data.questions
      .filter((question) => !answerValid(question))
      .map((question) => question.prompt);
  }

  function emptyAnswer(): DraftAnswer {
    return {
      selectedOptionKeys: [],
      otherText: '',
      delegatedDefault: false,
      otherOpen: false
    };
  }

  function initializeDraft(key: string) {
    initializedKey = key;
    const initial: Record<string, DraftAnswer> = {};
    for (const question of data.questions) initial[question.key] = emptyAnswer();
    if (draftEnabled) {
      try {
        const raw = localStorage.getItem(key);
        const saved = raw ? (JSON.parse(raw) as Record<string, Partial<DraftAnswer>>) : {};
        for (const question of data.questions) {
          const candidate = saved[question.key];
          if (!candidate) continue;
          const known = new Set(question.options.map((option) => option.key));
          initial[question.key] = {
            selectedOptionKeys: Array.isArray(candidate.selectedOptionKeys)
              ? candidate.selectedOptionKeys.filter(
                  (key): key is string => typeof key === 'string' && known.has(key)
                )
              : [],
            otherText:
              typeof candidate.otherText === 'string'
                ? Array.from(candidate.otherText).slice(0, 600).join('')
                : '',
            delegatedDefault:
              candidate.delegatedDefault === true && question.allowDelegatedDefault,
            otherOpen:
              candidate.otherOpen === true ||
              (typeof candidate.otherText === 'string' &&
                Boolean(candidate.otherText.trim()))
          };
        }
      } catch {
        localStorage.removeItem(key);
      }
    }
    answers = initial;
    localError = '';
  }

  function persistDraft() {
    if (!draftEnabled || !current) return;
    localStorage.setItem(draftKey, JSON.stringify(answers));
  }

  function clearDraft() {
    if (draftEnabled) localStorage.removeItem(draftKey);
  }

  function answerFor(question: ClarificationQuestion): DraftAnswer {
    return answers[question.key] || emptyAnswer();
  }

  function minimum(question: ClarificationQuestion): number {
    return question.minimumSelections ?? 1;
  }

  function maximum(question: ClarificationQuestion): number {
    if (question.selection === 'single_select') return 1;
    return question.maximumSelections ?? question.options.length;
  }

  function selectionCount(
    question: ClarificationQuestion,
    answer = answerFor(question)
  ): number {
    if (answer.delegatedDefault) return 1;
    return answer.selectedOptionKeys.length + (answer.otherText.trim() ? 1 : 0);
  }

  function answerValid(
    question: ClarificationQuestion,
    answer = answerFor(question)
  ): boolean {
    const count = selectionCount(question, answer);
    return count >= minimum(question) && count <= maximum(question);
  }

  function answerSummary(
    question: ClarificationQuestion,
    answer = answerFor(question)
  ): string {
    if (answer.delegatedDefault) {
      return t(
        '你帮我决定（采用合理、保守的默认值）',
        'Decide for me using a sensible conservative default'
      );
    }
    const knownOptions = new Map(
      question.options.map((option) => [option.key, option.label])
    );
    const labels = answer.selectedOptionKeys
      .map((key) => knownOptions.get(key) || '')
      .filter(Boolean);
    if (answer.otherText.trim()) {
      labels.push(
        t(
          `我的说明：${answer.otherText.trim()}`,
          `My note: ${answer.otherText.trim()}`
        )
      );
    }
    return labels.join(t('；', '; '));
  }

  function replaceAnswer(question: ClarificationQuestion, next: DraftAnswer) {
    answers = { ...answers, [question.key]: next };
    localError = '';
    persistDraft();
  }

  function toggleOption(question: ClarificationQuestion, optionKey: string) {
    if (interactionDisabled) return;
    const answer = answerFor(question);
    if (question.selection === 'single_select') {
      replaceAnswer(question, {
        selectedOptionKeys: [optionKey],
        otherText: '',
        delegatedDefault: false,
        otherOpen: false
      });
      return;
    }
    const selected = new Set(answer.selectedOptionKeys);
    if (selected.has(optionKey)) {
      selected.delete(optionKey);
    } else {
      // A delegated default is replaced by a concrete choice, so it must not
      // consume the multi-select limit while the user is switching answers.
      if (
        !answer.delegatedDefault &&
        selectionCount(question, answer) >= maximum(question)
      ) return;
      selected.add(optionKey);
    }
    replaceAnswer(question, {
      ...answer,
      selectedOptionKeys: [...selected],
      delegatedDefault: false
    });
  }

  function chooseDelegatedDefault(question: ClarificationQuestion) {
    if (interactionDisabled || !question.allowDelegatedDefault) return;
    replaceAnswer(question, {
      selectedOptionKeys: [],
      otherText: '',
      delegatedDefault: true,
      otherOpen: false
    });
  }

  async function toggleOther(question: ClarificationQuestion) {
    if (interactionDisabled) return;
    const answer = answerFor(question);
    if (answer.otherOpen) {
      replaceAnswer(question, { ...answer, otherOpen: false });
      return;
    }
    if (
      question.selection === 'multi_select' &&
      !answer.delegatedDefault &&
      selectionCount(question, answer) >= maximum(question)
    ) return;
    replaceAnswer(
      question,
      question.selection === 'single_select'
        ? {
            selectedOptionKeys: [],
            otherText: answer.otherText,
            delegatedDefault: false,
            otherOpen: true
          }
        : {
            ...answer,
            delegatedDefault: false,
            otherOpen: true
          }
    );
    await tick();
    document
      .getElementById(`guidance-other-${data.instanceId}-${question.key}`)
      ?.focus();
  }

  function updateOther(question: ClarificationQuestion, event: Event) {
    if (interactionDisabled) return;
    const value = (event.currentTarget as HTMLInputElement).value;
    const answer = answerFor(question);
    replaceAnswer(
      question,
      question.selection === 'single_select' && value.trim()
        ? {
            selectedOptionKeys: [],
            otherText: value,
            delegatedDefault: false,
            otherOpen: true
          }
        : {
            ...answer,
            otherText: value,
            delegatedDefault: false,
            otherOpen: true
          }
    );
  }

  function otherDisabled(
    question: ClarificationQuestion,
    answer = answerFor(question),
    unavailable = interactionDisabled
  ): boolean {
    return (
      unavailable ||
      (!answer.otherText && answer.selectedOptionKeys.length >= maximum(question))
    );
  }

  function submit(intent: 'continue_refining' | 'generate_from_current') {
    if (interactionDisabled || !complete) {
      localError = t(
        '请先回答每个问题；不确定时可以点“你帮我决定”。',
        'Answer each question first. Choose “Decide for me” when unsure.'
      );
      return;
    }
    const normalized: ClarificationAnswer[] = data.questions.map((question) => {
      const answer = answerFor(question);
      return {
        questionKey: question.key,
        selectedOptionKeys: [...answer.selectedOptionKeys],
        ...(answer.otherText.trim() ? { otherText: answer.otherText.trim() } : {}),
        ...(answer.delegatedDefault ? { delegatedDefault: true } : {})
      };
    });
    dispatch('submit', {
      submission: {
        sourceAssistantMessageId: sourceMessageId,
        sourcePartId,
        intent,
        answers: normalized
      }
    });
  }
</script>

<section
  class:readonly={!current}
  class="clarification-card"
  aria-label={t('需求澄清', 'Request clarification')}
>
  <header>
    <span aria-hidden="true"><Icon name="sparkles" size={16} /></span>
    <div>
      <strong>{t('把需求再说清一点', 'Refine the request')}</strong>
      {#if currentRoundNumber !== null && roundLimit !== null}
        <small class="round-progress">
          {t(
            `第 ${currentRoundNumber}/${roundLimit} 轮`,
            `Round ${currentRoundNumber} of ${roundLimit}`
          )}
        </small>
      {/if}
      {#if data.intro}<p>{data.intro}</p>{/if}
    </div>
  </header>

  {#if data.currentUnderstanding.length}
    <div class="current-understanding">
      <b>{t('我目前理解的是', 'Current understanding')}</b>
      <ul>
        {#each data.currentUnderstanding as item}<li>{item}</li>{/each}
      </ul>
    </div>
  {/if}

  <div class="question-list">
    {#each data.questions as question, questionIndex (question.key)}
      {@const answer = answers[question.key] || emptyAnswer()}
      {@const count = selectionCount(question, answer)}
      {@const max = maximum(question)}
      <fieldset>
        <legend>
          <span>{questionIndex + 1}</span>
          {question.prompt}
        </legend>
        {#if question.selection === 'multi_select'}
          <small class="selection-count">
            {t(`已选 ${count} 项，最多 ${max} 项`, `${count} selected, up to ${max}`)}
          </small>
        {/if}
        <div
          class="option-grid"
          role={question.selection === 'single_select' ? 'radiogroup' : 'group'}
          aria-label={question.prompt}
        >
          {#each question.options as option (option.key)}
            {@const selected = answer.selectedOptionKeys.includes(option.key)}
            {@const atLimit =
              question.selection === 'multi_select' &&
              !answer.delegatedDefault &&
              !selected &&
              count >= max}
            <button
              type="button"
              class:selected
              role={question.selection === 'single_select' ? 'radio' : 'checkbox'}
              aria-checked={selected}
              disabled={interactionDisabled || atLimit}
              on:click={() => toggleOption(question, option.key)}
            >
              <span class="choice-mark" aria-hidden="true">
                {#if selected}<Icon name="check" size={14} />{/if}
              </span>
              <span>
                <strong>{option.label}</strong>
                {#if option.description}<small>{option.description}</small>{/if}
              </span>
            </button>
          {/each}
          {#if question.allowOther}
            <button
              type="button"
              class:selected={Boolean(answer.otherText.trim())}
              class:expanded={answer.otherOpen}
              role={question.selection === 'single_select' ? 'radio' : 'checkbox'}
              aria-checked={Boolean(answer.otherText.trim())}
              aria-expanded={answer.otherOpen}
              aria-controls={`guidance-other-${data.instanceId}-${question.key}`}
              disabled={interactionDisabled ||
                (question.selection === 'multi_select' &&
                  !answer.delegatedDefault &&
                  !answer.otherOpen &&
                  !answer.otherText &&
                  count >= max)}
              on:click={() => toggleOther(question)}
            >
              <span class="choice-mark" aria-hidden="true">
                {#if answer.otherText.trim()}<Icon name="check" size={14} />{/if}
              </span>
              <span>
                <strong>{t('其他 / 我来说明', 'Other / I’ll explain')}</strong>
                <small>{t('点击后输入自己的情况', 'Tap to enter your own answer')}</small>
              </span>
            </button>
          {/if}
          {#if question.allowDelegatedDefault}
            <button
              type="button"
              class:selected={answer.delegatedDefault}
              role={question.selection === 'single_select' ? 'radio' : 'checkbox'}
              aria-checked={answer.delegatedDefault}
              disabled={interactionDisabled}
              on:click={() => chooseDelegatedDefault(question)}
            >
              <span class="choice-mark" aria-hidden="true">
                {#if answer.delegatedDefault}<Icon name="check" size={14} />{/if}
              </span>
              <span>
                <strong>{t('你帮我决定', 'Decide for me')}</strong>
                <small>{t('采用合理、保守的默认值', 'Use a sensible conservative default')}</small>
              </span>
            </button>
          {/if}
        </div>
        {#if question.allowOther && answer.otherOpen}
          <label class="other-answer">
            <span>{t('其他 / 我来说明', 'Other / I’ll explain')}</span>
            <input
              id={`guidance-other-${data.instanceId}-${question.key}`}
              type="text"
              value={answer.otherText}
              maxlength="600"
              disabled={otherDisabled(question, answer, interactionDisabled)}
              aria-describedby={otherDisabled(
                question,
                answer,
                interactionDisabled
              ) && !interactionDisabled
                ? `guidance-help-${data.instanceId}-${question.key}`
                : undefined}
              on:input={(event) => updateOther(question, event)}
            />
          </label>
          {#if otherDisabled(
            question,
            answer,
            interactionDisabled
          ) && !interactionDisabled}
            <small id={`guidance-help-${data.instanceId}-${question.key}`} class="limit-help">
              {t('已达到本题上限，先取消一个选项再填写。', 'The limit is reached; deselect an option first.')}
            </small>
          {/if}
        {/if}
      </fieldset>
    {/each}
  </div>

  {#if current}
    <section class="current-preview" aria-live="polite">
      <div class="preview-heading">
        <strong>{t('当前简报预览', 'Current brief preview')}</strong>
        <small>
          {t(
            '选择“直接生成”将确认下面这些内容，不会把未标明的猜测当成你的事实。',
            'Generating now confirms only the details below; unlisted guesses are not treated as your facts.'
          )}
        </small>
      </div>
      {#if data.currentUnderstanding.length}
        <div class="preview-section">
          <b>{t('当前任务与已确认背景', 'Task and confirmed context')}</b>
          <ul>
            {#each data.currentUnderstanding as item}<li>{item}</li>{/each}
          </ul>
        </div>
      {/if}
      {#if previewRows.length}
        <div class="preview-section">
          <b>{t('本轮选择', 'Selections this round')}</b>
          <dl>
            {#each previewRows as row}
              <div>
                <dt>{row.prompt}</dt>
                <dd>{row.answer}</dd>
              </div>
            {/each}
          </dl>
        </div>
      {/if}
      {#if previewUnresolved.length}
        <div class="preview-section unresolved">
          <b>{t('尚未确认', 'Still unanswered')}</b>
          <ul>
            {#each previewUnresolved as item}<li>{item}</li>{/each}
          </ul>
        </div>
      {/if}
    </section>
  {/if}

  {#if localError}<p class="card-error" role="alert">{localError}</p>{/if}
  {#if current}
    <footer>
      <button
        type="button"
        class="secondary-action"
        disabled={interactionDisabled || !complete}
        on:click={() => submit('continue_refining')}
      >
        {finalRound
          ? t('形成任务简报', 'Create task brief')
          : t('继续完善', 'Keep refining')}
      </button>
      <button
        type="button"
        class="primary-action"
        disabled={interactionDisabled || !complete}
        on:click={() => submit('generate_from_current')}
      >
        <Icon name="send" size={15} />
        {t('按当前选择直接生成', 'Generate from these choices')}
      </button>
    </footer>
  {:else}
    <p class="readonly-note">
      <Icon name="check" size={14} />
      {t('本轮已提交或已被后续消息替代', 'This round was submitted or superseded')}
    </p>
  {/if}
</section>

<style>
  .clarification-card {
    width: min(100%, 760px);
    margin: 8px 0 14px;
    overflow: hidden;
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 16px;
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .clarification-card > header {
    display: flex;
    gap: 10px;
    padding: 16px;
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

  header strong {
    display: block;
    font-size: 1rem;
  }

  .round-progress {
    display: inline-flex;
    margin-top: 5px;
    padding: 2px 8px;
    color: var(--primary);
    border-radius: 999px;
    background: var(--primary-soft);
    font-size: 0.75rem;
    font-weight: 700;
  }

  header p {
    margin: 4px 0 0;
    color: var(--text-soft);
    font-size: 0.9rem;
  }

  .current-understanding {
    padding: 14px 16px 2px;
    overflow-wrap: anywhere;
    color: var(--text-soft);
    font-size: 0.9rem;
  }

  .current-understanding b {
    color: var(--text);
  }

  .current-understanding ul {
    margin: 6px 0 0;
    padding-left: 20px;
  }

  .question-list {
    display: grid;
    gap: 0;
    padding: 4px 16px;
  }

  fieldset {
    min-width: 0;
    margin: 0;
    padding: 16px 0;
    border: 0;
    border-bottom: 1px solid var(--border);
  }

  fieldset:last-child {
    border-bottom: 0;
  }

  legend {
    display: flex;
    width: 100%;
    gap: 8px;
    padding: 0;
    font-weight: 650;
    line-height: 1.5;
  }

  legend > span {
    display: inline-grid;
    width: 24px;
    height: 24px;
    flex: 0 0 24px;
    place-items: center;
    color: var(--primary);
    border-radius: 8px;
    background: var(--primary-soft);
    font-size: 0.78rem;
  }

  .selection-count,
  .limit-help {
    display: block;
    margin: 6px 0 0 32px;
    color: var(--text-muted);
  }

  .option-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
    margin-top: 12px;
  }

  .option-grid button {
    display: flex;
    min-height: 52px;
    gap: 10px;
    align-items: flex-start;
    padding: 10px 12px;
    color: var(--text);
    text-align: left;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
    cursor: pointer;
    transition:
      border-color 180ms ease,
      background 180ms ease;
  }

  .option-grid button:hover:not(:disabled) {
    border-color: var(--border-strong);
    background: var(--surface-soft);
  }

  .option-grid button.selected {
    border-color: var(--primary);
    background: var(--primary-soft);
  }

  .option-grid button.expanded:not(.selected) {
    border-color: var(--border-strong);
    background: var(--surface-soft);
  }

  .option-grid button:disabled {
    opacity: 0.56;
  }

  .choice-mark {
    display: grid;
    width: 20px;
    height: 20px;
    flex: 0 0 20px;
    place-items: center;
    margin-top: 1px;
    color: var(--primary-contrast);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    background: var(--surface);
  }

  button.selected .choice-mark {
    border-color: var(--primary);
    background: var(--primary);
  }

  .option-grid strong,
  .option-grid small {
    display: block;
  }

  .option-grid button > span:last-child {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .option-grid strong {
    font-size: 0.92rem;
    line-height: 1.35;
  }

  .option-grid small {
    margin-top: 3px;
    color: var(--text-soft);
    font-size: 0.8rem;
    line-height: 1.4;
  }

  .other-answer {
    display: grid;
    gap: 6px;
    margin-top: 12px;
    color: var(--text-soft);
    font-size: 0.86rem;
    font-weight: 600;
  }

  .other-answer input {
    width: 100%;
    min-height: 44px;
    padding: 9px 12px;
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface);
    font-size: 1rem;
  }

  .other-answer input:focus {
    border-color: var(--primary);
  }

  .current-preview {
    display: grid;
    gap: 12px;
    margin: 4px 16px 16px;
    padding: 14px;
    overflow-wrap: anywhere;
    border: 1px solid color-mix(in srgb, var(--primary) 26%, var(--border));
    border-radius: 12px;
    background: color-mix(in srgb, var(--primary-soft) 55%, var(--surface));
  }

  .preview-heading strong,
  .preview-heading small,
  .preview-section b {
    display: block;
  }

  .preview-heading strong {
    font-size: 0.92rem;
  }

  .preview-heading small {
    margin-top: 3px;
    color: var(--text-soft);
    font-size: 0.78rem;
    line-height: 1.45;
  }

  .preview-section {
    min-width: 0;
  }

  .preview-section b {
    color: var(--text-soft);
    font-size: 0.78rem;
  }

  .preview-section ul {
    margin: 6px 0 0;
    padding-left: 19px;
    font-size: 0.84rem;
  }

  .preview-section dl {
    display: grid;
    gap: 8px;
    margin: 7px 0 0;
  }

  .preview-section dl > div {
    display: grid;
    gap: 2px;
  }

  .preview-section dt {
    color: var(--text-soft);
    font-size: 0.77rem;
  }

  .preview-section dd {
    margin: 0;
    font-size: 0.86rem;
    font-weight: 650;
  }

  .preview-section.unresolved {
    color: var(--text-muted);
  }

  .card-error {
    margin: 0 16px 12px;
    color: var(--danger);
    font-size: 0.88rem;
  }

  footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 14px 16px 16px;
    border-top: 1px solid var(--border);
    background: var(--surface-soft);
  }

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

  footer button:disabled {
    cursor: not-allowed;
    opacity: 0.5;
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
    .option-grid {
      grid-template-columns: 1fr;
    }

    footer {
      flex-direction: column-reverse;
    }

    footer button {
      width: 100%;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .option-grid button {
      transition: none;
    }
  }
</style>
