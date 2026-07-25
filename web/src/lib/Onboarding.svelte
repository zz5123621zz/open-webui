<script lang="ts">
  import { createEventDispatcher, onMount, tick } from 'svelte';
  import Icon from './Icon.svelte';
  import { translate, type Locale } from './i18n';

  export let locale: Locale = 'zh-CN';

  type StepIcon = 'sparkles' | 'plan' | 'chat' | 'upload';
  type Step = {
    icon: StepIcon;
    chineseEyebrow: string;
    englishEyebrow: string;
    chineseTitle: string;
    englishTitle: string;
    chineseDescription: string;
    englishDescription: string;
  };

  const steps: Step[] = [
    {
      icon: 'sparkles',
      chineseEyebrow: '模型',
      englishEyebrow: 'Model',
      chineseTitle: '先选适合这次任务的模型',
      englishTitle: 'Choose the right model for this task',
      chineseDescription:
        '点击顶部模型名称即可切换。Sol 智能最高，Terra 更均衡，Luna 响应更轻快；选择会保存在当前会话中。',
      englishDescription:
        'Use the model name at the top. Sol is the most capable, Terra is balanced, and Luna is faster. The choice is saved per chat.'
    },
    {
      icon: 'plan',
      chineseEyebrow: '推理强度',
      englishEyebrow: 'Reasoning',
      chineseTitle: '再决定需要思考多深',
      englishTitle: 'Then choose how deeply to reason',
      chineseDescription:
        '低适合日常问答，中兼顾速度与质量，高适合复杂任务。强度越高通常等待越久，也会使用更多额度。',
      englishDescription:
        'Low suits everyday questions, Medium balances speed and quality, and High is for complex work. Higher levels usually take longer and use more quota.'
    },
    {
      icon: 'chat',
      chineseEyebrow: '会话',
      englishEyebrow: 'Chats',
      chineseTitle: '把重要对话整理好',
      englishTitle: 'Keep important chats organized',
      chineseDescription:
        '侧栏可以新建、重命名、置顶或临时留档会话。置顶会话不会被自动顶掉，临时留档在 7 天内可以恢复。',
      englishDescription:
        'Use the sidebar to create, rename, pin, or retain chats. Pinned chats are protected, and retained chats can be restored for seven days.'
    },
    {
      icon: 'upload',
      chineseEyebrow: '输入与结果',
      englishEyebrow: 'Input & results',
      chineseTitle: '从输入框开始，过程随时可查',
      englishTitle: 'Start in the composer and inspect the process',
      chineseDescription:
        '可以上传图片或开启图片生成；需要时模型会联网。推理摘要、搜索内容和来源会显示在回答附近，但不会展示私密思维链。',
      englishDescription:
        'Upload an image or enable image generation; the model can search when needed. Safe reasoning summaries, queries, and sources stay near the answer without exposing private chain of thought.'
    }
  ];

  const dispatch = createEventDispatcher<{ dismiss: void }>();
  let stepIndex = 0;
  let dialogElement: HTMLDivElement;

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: currentStep = steps[stepIndex];
  $: isLastStep = stepIndex === steps.length - 1;

  onMount(async () => {
    await tick();
    dialogElement?.focus();
  });

  function close() {
    dispatch('dismiss');
  }

  function next() {
    if (isLastStep) {
      close();
      return;
    }
    stepIndex += 1;
    void tick().then(() => dialogElement?.focus());
  }

  function previous() {
    if (stepIndex === 0) return;
    stepIndex -= 1;
    void tick().then(() => dialogElement?.focus());
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      close();
      return;
    }
    if (event.key !== 'Tab') return;
    const focusable = Array.from(
      dialogElement.querySelectorAll<HTMLElement>(
        'button:not([disabled]), [href], input:not([disabled]), [tabindex]:not([tabindex="-1"])'
      )
    );
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable.at(-1) as HTMLElement;
    if (
      event.shiftKey &&
      (document.activeElement === first || document.activeElement === dialogElement)
    ) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
</script>

<div class="onboarding-layer">
  <button
    type="button"
    class="onboarding-backdrop"
    aria-label={t('跳过新手指南', 'Skip getting started')}
    on:click={close}
  ></button>
  <div
    class="onboarding-dialog"
    bind:this={dialogElement}
    role="dialog"
    aria-modal="true"
    aria-labelledby="onboarding-title"
    aria-describedby="onboarding-description"
    tabindex="-1"
    on:keydown={handleKeydown}
  >
    <header>
      <div class="onboarding-brand">
        <span><Icon name="sparkles" size={18} /></span>
        <div>
          <strong>La4RainGPT</strong>
          <small>{t('首次使用指南', 'Getting started')}</small>
        </div>
      </div>
      <button type="button" class="skip-button" on:click={close}>
        {t('跳过', 'Skip')}
      </button>
    </header>

    <div class="onboarding-progress" aria-label={t('指南进度', 'Guide progress')}>
      {#each steps as step, index}
        <span
          class:active={index === stepIndex}
          class:complete={index < stepIndex}
          aria-current={index === stepIndex ? 'step' : undefined}
          aria-label={t(
            `第 ${index + 1} 步：${step.chineseEyebrow}`,
            `Step ${index + 1}: ${step.englishEyebrow}`
          )}
        ></span>
      {/each}
    </div>

    <section class="onboarding-content">
      <div class="step-visual" aria-hidden="true">
        {#if stepIndex === 0}
          <div class="mock-topbar">
            <span class="mock-control selected">GPT 5.6 Sol <Icon name="chevron-down" size={13} /></span>
            <span class="mock-control">{t('推理 · 中', 'Reasoning · Medium')}</span>
          </div>
          <div class="tier-row">
            <span><b>Sol</b>{t('旗舰', 'Flagship')}</span>
            <i>›</i>
            <span><b>Terra</b>{t('均衡', 'Balanced')}</span>
            <i>›</i>
            <span><b>Luna</b>{t('轻快', 'Fast')}</span>
          </div>
        {:else if stepIndex === 1}
          <div class="effort-demo">
            <span><b>{t('低', 'Low')}</b><small>medium</small></span>
            <span class="selected"><b>{t('中', 'Medium')}</b><small>high</small></span>
            <span><b>{t('高', 'High')}</b><small>max</small></span>
          </div>
        {:else if stepIndex === 2}
          <div class="chat-demo">
            <span><Icon name="plus" size={15} />{t('新对话', 'New chat')}</span>
            <span><Icon name="edit" size={15} />{t('重命名', 'Rename')}</span>
            <span><Icon name="pin" size={15} />{t('置顶', 'Pin')}</span>
            <span><Icon name="archive" size={15} />{t('临时留档', 'Retain')}</span>
          </div>
        {:else}
          <div class="composer-demo">
            <span>{t('给 AI 发送消息', 'Message AI')}</span>
            <div>
              <i><Icon name="upload" size={15} /></i>
              <i><Icon name="image-plus" size={15} /></i>
              <b><Icon name="send" size={16} /></b>
            </div>
          </div>
          <div class="process-demo">
            <span><Icon name="sparkles" size={13} />{t('推理摘要', 'Reasoning summary')}</span>
            <span><Icon name="search" size={13} />{t('搜索与来源', 'Search & sources')}</span>
          </div>
        {/if}
      </div>

      <div class="step-copy">
        <span class="step-icon"><Icon name={currentStep.icon} size={19} /></span>
        <small>
          {t(currentStep.chineseEyebrow, currentStep.englishEyebrow)}
          · {stepIndex + 1}/{steps.length}
        </small>
        <h2 id="onboarding-title">
          {t(currentStep.chineseTitle, currentStep.englishTitle)}
        </h2>
        <p id="onboarding-description">
          {t(currentStep.chineseDescription, currentStep.englishDescription)}
        </p>
      </div>
    </section>

    <footer>
      <p>{t('以后可从头像菜单重新打开', 'Reopen this guide from the profile menu')}</p>
      <div>
        {#if stepIndex > 0}
          <button type="button" class="secondary-button" on:click={previous}>
            {t('上一步', 'Back')}
          </button>
        {/if}
        <button type="button" class="next-button" on:click={next}>
          {isLastStep ? t('开始聊天', 'Start chatting') : t('下一步', 'Next')}
          {#if !isLastStep}<span aria-hidden="true">→</span>{/if}
        </button>
      </div>
    </footer>
  </div>
</div>

<style>
  .onboarding-layer {
    position: fixed;
    z-index: 100;
    display: grid;
    padding: 24px;
    inset: 0;
    place-items: center;
  }

  .onboarding-backdrop {
    position: absolute;
    padding: 0;
    border: 0;
    background: rgba(15, 20, 27, 0.52);
    inset: 0;
    backdrop-filter: blur(3px);
  }

  .onboarding-dialog {
    position: relative;
    z-index: 1;
    width: min(100%, 620px);
    max-height: min(760px, calc(100dvh - 48px));
    overflow: auto;
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 22px;
    background: var(--surface);
    box-shadow: var(--shadow-lg);
  }

  header,
  footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 20px 22px;
  }

  header {
    border-bottom: 1px solid var(--border);
  }

  .onboarding-brand {
    display: flex;
    align-items: center;
    gap: 11px;
  }

  .onboarding-brand > span,
  .step-icon {
    display: grid;
    width: 40px;
    height: 40px;
    flex: 0 0 auto;
    place-items: center;
    color: var(--primary);
    border: 1px solid color-mix(in srgb, var(--primary) 20%, var(--border));
    border-radius: 12px;
    background: var(--primary-soft);
  }

  .onboarding-brand > div {
    display: grid;
    gap: 1px;
  }

  .onboarding-brand strong {
    font-size: 14px;
    font-weight: 700;
  }

  .onboarding-brand small {
    color: var(--text-muted);
    font-size: 11px;
  }

  button {
    min-height: 44px;
    cursor: pointer;
    border: 0;
    font: inherit;
  }

  .skip-button {
    padding: 0 12px;
    color: var(--text-muted);
    border-radius: 10px;
    background: transparent;
    font-size: 12px;
  }

  .skip-button:hover {
    color: var(--text);
    background: var(--surface-hover);
  }

  .onboarding-progress {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 6px;
    padding: 14px 22px 0;
  }

  .onboarding-progress span {
    height: 3px;
    border-radius: 999px;
    background: var(--border);
  }

  .onboarding-progress span.active,
  .onboarding-progress span.complete {
    background: var(--primary);
  }

  .onboarding-content {
    display: grid;
    grid-template-columns: minmax(0, 1.05fr) minmax(0, 0.95fr);
    gap: 24px;
    min-height: 310px;
    padding: 22px;
  }

  .step-visual {
    display: flex;
    min-width: 0;
    flex-direction: column;
    justify-content: center;
    gap: 18px;
    overflow: hidden;
    padding: 24px;
    border: 1px solid var(--border);
    border-radius: 18px;
    background:
      radial-gradient(circle at 80% 10%, color-mix(in srgb, var(--primary) 9%, transparent), transparent 46%),
      var(--canvas);
  }

  .mock-topbar,
  .tier-row,
  .effort-demo,
  .chat-demo,
  .composer-demo,
  .process-demo {
    display: flex;
    align-items: center;
  }

  .mock-topbar {
    flex-wrap: wrap;
    gap: 8px;
  }

  .mock-control {
    display: inline-flex;
    min-height: 40px;
    align-items: center;
    gap: 8px;
    padding: 0 11px;
    color: var(--text-soft);
    border: 1px solid var(--border);
    border-radius: 11px;
    background: var(--surface);
    box-shadow: var(--shadow-sm);
    font-size: 11px;
    font-weight: 650;
  }

  .mock-control.selected {
    color: var(--primary);
    border-color: color-mix(in srgb, var(--primary) 32%, var(--border));
  }

  .tier-row {
    justify-content: space-between;
    gap: 6px;
  }

  .tier-row span,
  .effort-demo span {
    display: grid;
    min-width: 0;
    gap: 2px;
    text-align: center;
  }

  .tier-row b {
    font-size: 13px;
  }

  .tier-row span {
    color: var(--text-muted);
    font-size: 9px;
  }

  .tier-row i {
    color: var(--text-faint);
    font-style: normal;
  }

  .effort-demo {
    justify-content: center;
    gap: 7px;
  }

  .effort-demo span {
    width: 72px;
    padding: 14px 8px;
    color: var(--text-soft);
    border: 1px solid var(--border);
    border-radius: 13px;
    background: var(--surface);
  }

  .effort-demo span.selected {
    color: var(--primary);
    border-color: color-mix(in srgb, var(--primary) 35%, var(--border));
    background: var(--primary-soft);
  }

  .effort-demo b {
    font-size: 14px;
  }

  .effort-demo small {
    color: var(--text-muted);
    font-size: 9px;
  }

  .chat-demo {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px;
  }

  .chat-demo span,
  .process-demo span {
    display: flex;
    min-height: 44px;
    align-items: center;
    gap: 7px;
    padding: 0 10px;
    color: var(--text-soft);
    border: 1px solid var(--border);
    border-radius: 11px;
    background: var(--surface);
    font-size: 10px;
  }

  .composer-demo {
    display: grid;
    gap: 18px;
    padding: 14px;
    color: var(--text-muted);
    border: 1px solid var(--border-strong);
    border-radius: 15px;
    background: var(--surface);
    box-shadow: var(--shadow-md);
    font-size: 11px;
  }

  .composer-demo > div {
    display: flex;
    align-items: center;
    gap: 7px;
  }

  .composer-demo i,
  .composer-demo b {
    display: grid;
    width: 36px;
    height: 36px;
    place-items: center;
    color: var(--text-soft);
    border-radius: 10px;
    background: var(--surface-soft);
    font-style: normal;
  }

  .composer-demo b {
    margin-left: auto;
    color: var(--primary-contrast);
    background: var(--primary);
  }

  .process-demo {
    flex-wrap: wrap;
    gap: 7px;
  }

  .process-demo span {
    min-height: 36px;
    color: var(--primary);
    background: var(--primary-soft);
  }

  .step-copy {
    display: flex;
    min-width: 0;
    flex-direction: column;
    justify-content: center;
    align-items: flex-start;
  }

  .step-icon {
    margin-bottom: 15px;
  }

  .step-copy > small {
    color: var(--primary);
    font-size: 11px;
    font-weight: 670;
  }

  h2 {
    margin: 8px 0 10px;
    font-size: clamp(21px, 4vw, 27px);
    letter-spacing: -0.035em;
    line-height: 1.28;
  }

  .step-copy p {
    margin: 0;
    color: var(--text-soft);
    font-size: 13px;
    line-height: 1.72;
  }

  footer {
    gap: 16px;
    border-top: 1px solid var(--border);
  }

  footer p {
    margin: 0;
    color: var(--text-muted);
    font-size: 10px;
  }

  footer > div {
    display: flex;
    flex: 0 0 auto;
    gap: 8px;
  }

  .secondary-button,
  .next-button {
    min-width: 88px;
    padding: 0 15px;
    border-radius: 11px;
    font-size: 12px;
    font-weight: 650;
  }

  .secondary-button {
    color: var(--text-soft);
    border: 1px solid var(--border);
    background: var(--surface);
  }

  .next-button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    color: var(--primary-contrast);
    background: var(--primary);
  }

  .secondary-button:hover {
    background: var(--surface-hover);
  }

  .next-button:hover {
    background: var(--primary-hover);
  }

  .secondary-button:active,
  .next-button:active,
  .skip-button:active {
    transform: scale(0.97);
  }

  @media (max-width: 620px) {
    .onboarding-layer {
      align-items: end;
      padding: 0;
    }

    .onboarding-dialog {
      width: 100%;
      max-height: min(92dvh, 760px);
      border-right: 0;
      border-bottom: 0;
      border-left: 0;
      border-radius: 22px 22px 0 0;
    }

    header {
      padding: 15px 16px;
    }

    .onboarding-progress {
      padding: 12px 16px 0;
    }

    .onboarding-content {
      grid-template-columns: 1fr;
      gap: 18px;
      min-height: 0;
      padding: 16px;
    }

    .step-visual {
      min-height: 156px;
      padding: 18px;
    }

    .step-copy {
      justify-content: start;
    }

    .step-icon {
      display: none;
    }

    h2 {
      margin-top: 6px;
      font-size: 22px;
    }

    footer {
      align-items: flex-end;
      padding: 14px 16px max(14px, env(safe-area-inset-bottom));
    }

    footer p {
      max-width: 130px;
    }
  }

  @media (max-width: 380px) {
    .step-visual {
      min-height: 140px;
      padding: 14px;
    }

    .tier-row span {
      font-size: 8px;
    }

    footer p {
      display: none;
    }

    footer > div {
      width: 100%;
    }

    .secondary-button,
    .next-button {
      flex: 1;
    }
  }

  @media (hover: hover) and (pointer: fine) {
    .skip-button,
    .secondary-button,
    .next-button {
      transition:
        color 160ms cubic-bezier(0.16, 1, 0.3, 1),
        background 160ms cubic-bezier(0.16, 1, 0.3, 1),
        border-color 160ms cubic-bezier(0.16, 1, 0.3, 1),
        transform 160ms cubic-bezier(0.16, 1, 0.3, 1);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .skip-button,
    .secondary-button,
    .next-button {
      transition: none;
    }
  }
</style>
