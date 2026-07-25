<script lang="ts">
  import { onMount } from 'svelte';
  import {
    getProgressiveSummarySettings,
    recheckProgressiveSummaryCompatibility,
    updateProgressiveSummarySettings
  } from './api';
  import Icon from './Icon.svelte';
  import { translate, type Locale } from './i18n';
  import type {
    ProgressiveSummaryMode,
    ProgressiveSummarySettings,
    ProgressiveSummaryState
  } from './types';

  export let locale: Locale = 'zh-CN';

  let settings: ProgressiveSummarySettings | null = null;
  let selectedMode: ProgressiveSummaryMode = 'auto';
  let pending = false;
  let error = '';
  let notice = '';

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: dirty = Boolean(settings && selectedMode !== settings.mode);

  onMount(() => {
    void load();
  });

  async function load() {
    pending = true;
    error = '';
    try {
      const next = await getProgressiveSummarySettings();
      settings = next;
      selectedMode = next.mode;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : t('读取设置失败。', 'Could not load settings.');
    } finally {
      pending = false;
    }
  }

  async function save() {
    if (!dirty || pending) return;
    pending = true;
    error = '';
    notice = '';
    try {
      const next = await updateProgressiveSummarySettings(selectedMode);
      settings = next;
      selectedMode = next.mode;
      notice = t(
        '设置已保存，只影响之后开始的新回答。',
        'Saved. This only affects responses started from now on.'
      );
    } catch (cause) {
      error = cause instanceof Error ? cause.message : t('保存设置失败。', 'Could not save settings.');
    } finally {
      pending = false;
    }
  }

  async function recheck() {
    if (pending) return;
    pending = true;
    error = '';
    notice = '';
    try {
      const next = await recheckProgressiveSummaryCompatibility();
      settings = next;
      selectedMode = next.mode;
      notice = t(
        '兼容状态已清除；下一次符合条件的正常聊天会进行检测，不会额外消耗一次请求。',
        'Compatibility state cleared. The next eligible normal chat will probe; no extra request was sent.'
      );
    } catch (cause) {
      error = cause instanceof Error ? cause.message : t('重新检测失败。', 'Could not reset detection.');
    } finally {
      pending = false;
    }
  }

  function stateLabel(state: ProgressiveSummaryState): string {
    const labels: Record<ProgressiveSummaryState, [string, string]> = {
      unknown: ['等待检测', 'Awaiting probe'],
      probing: ['检测中', 'Probing'],
      active: ['可用', 'Available'],
      cooldown: ['暂时不兼容', 'Temporarily incompatible'],
      disabled: ['已关闭', 'Disabled'],
      mixed: ['各模型状态不同', 'Mixed by model']
    };
    return t(...labels[state]);
  }

  function formatTime(value?: number): string {
    if (!value) return t('尚未检测', 'Not checked yet');
    return new Intl.DateTimeFormat(locale, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit'
    }).format(new Date(value));
  }

  function cooldownLabel(value?: number): string {
    if (!value) return '';
    const remainingMinutes = Math.max(0, Math.ceil((value - Date.now()) / 60000));
    return remainingMinutes > 0
      ? t(`约 ${remainingMinutes} 分钟后可重试`, `Retry available in about ${remainingMinutes} min`)
      : t('可重新检测', 'Ready to recheck');
  }
</script>

<div class="service-settings">
  <div class="dialog-icon"><Icon name="sparkles" size={23} /></div>
  <h2 id="dialog-title">{t('渐进式推理摘要', 'Progressive reasoning summaries')}</h2>
  <p class="service-lead">
    {t(
      '在上游支持时尽早展示 Provider 提供的安全摘要。不会显示原始思维链，也不会编造思考过程。',
      'Show provider-authored safe summaries earlier when supported. Raw chain of thought is never shown or fabricated.'
    )}
  </p>

  {#if !settings && pending}
    <div class="service-loading" role="status">
      <span class="tool-spinner" aria-hidden="true"></span>
      {t('正在读取服务设置…', 'Loading service settings…')}
    </div>
  {:else if settings}
    <section class="service-card" aria-labelledby="summary-mode-title">
      <div class="service-card-heading">
        <div>
          <strong id="summary-mode-title">{t('新回答的处理方式', 'Mode for new responses')}</strong>
          <span>{t('切换无需重启应用或 CPA', 'No app or CPA restart required')}</span>
        </div>
        <span class={`state-badge ${settings.effectiveState}`}>
          {stateLabel(settings.effectiveState)}
        </span>
      </div>

      <div class="mode-options" role="radiogroup" aria-label={t('摘要模式', 'Summary mode')}>
        <button
          type="button"
          role="radio"
          aria-checked={selectedMode === 'auto'}
          class:selected={selectedMode === 'auto'}
          disabled={pending}
          on:click={() => (selectedMode = 'auto')}
        >
          <span class="mode-check"><Icon name={selectedMode === 'auto' ? 'check' : 'sparkles'} size={15} /></span>
          <span>
            <strong>{t('自动', 'Auto')}</strong>
            <small>{t('兼容时启用，不兼容时自动回退', 'Enable when compatible and fall back safely')}</small>
          </span>
        </button>
        <button
          type="button"
          role="radio"
          aria-checked={selectedMode === 'off'}
          class:selected={selectedMode === 'off'}
          disabled={pending}
          on:click={() => (selectedMode = 'off')}
        >
          <span class="mode-check"><Icon name={selectedMode === 'off' ? 'check' : 'stop'} size={15} /></span>
          <span>
            <strong>{t('关闭', 'Off')}</strong>
            <small>{t('使用普通 CPA 流式响应', 'Use the standard CPA response stream')}</small>
          </span>
        </button>
      </div>

      {#if settings.hardDisabled}
        <div class="hard-disabled" role="status">
          <Icon name="alert" size={15} />
          <span>
            {t(
              '部署级紧急关闭已启用。管理员页面不能越过这一安全上限。',
              'The deployment emergency switch is active. This page cannot override that safety ceiling.'
            )}
          </span>
        </div>
      {/if}

      <button class="service-save" type="button" disabled={!dirty || pending} on:click={save}>
        {pending ? t('正在处理…', 'Working…') : t('保存设置', 'Save setting')}
      </button>
      <p class="service-boundary">
        {t(
          '设置变化不会取消或改写正在生成的回答。',
          'Changing this setting does not cancel or alter an in-flight response.'
        )}
      </p>
    </section>

    <section class="service-card compatibility-card" aria-labelledby="compatibility-title">
      <div class="service-card-heading">
        <div>
          <strong id="compatibility-title">{t('模型兼容状态', 'Model compatibility')}</strong>
          <span>{t('按 CPA 地址和模型分别记录', 'Tracked separately by CPA endpoint and model')}</span>
        </div>
        <button type="button" class="recheck-button" disabled={pending} on:click={recheck}>
          <Icon name="refresh" size={15} />
          {t('重新检测', 'Recheck')}
        </button>
      </div>

      {#if settings.models.length}
        <div class="model-status-list">
          {#each settings.models as model (model.model)}
            <div>
              <span class={`model-state ${model.state}`} aria-hidden="true"></span>
              <span class="model-status-copy">
                <strong>{model.model}</strong>
                <small>
                  {stateLabel(model.state)} · {formatTime(model.lastCheckedAt)}
                  {#if model.state === 'cooldown' && cooldownLabel(model.cooldownUntil)}
                    · {cooldownLabel(model.cooldownUntil)}
                  {/if}
                </small>
              </span>
            </div>
          {/each}
        </div>
      {:else}
        <div class="empty-status">
          {t(
            '还没有检测记录。启用“自动”后，下一次符合条件的正常聊天会成为唯一探针。',
            'No detection record yet. With Auto enabled, the next eligible normal chat becomes the single probe.'
          )}
        </div>
      {/if}
      <p class="service-boundary">
        {t(
          '重新检测只清除本地状态，不会发送隐藏提示词，也不会产生额外额度消耗。',
          'Recheck only clears local state. It sends no hidden prompt and consumes no extra quota.'
        )}
      </p>
    </section>
  {/if}

  {#if notice}<div class="service-notice" role="status">{notice}</div>{/if}
  {#if error}<div class="account-error" role="alert">{error}</div>{/if}
</div>

<style>
  .service-settings {
    color: var(--text);
  }

  .service-settings h2 {
    margin: 14px 0 0;
    font-size: 1.375rem;
  }

  .service-lead {
    margin: 9px 0 20px;
    color: var(--text-soft);
    font-size: 0.8125rem;
    line-height: 1.65;
  }

  .service-loading {
    display: flex;
    min-height: 120px;
    align-items: center;
    justify-content: center;
    gap: 10px;
    color: var(--text-muted);
    font-size: 0.8125rem;
  }

  .service-card {
    padding: 14px;
    border: 1px solid var(--border);
    border-radius: 14px;
    background: var(--surface-soft);
  }

  .service-card + .service-card {
    margin-top: 12px;
  }

  .service-card-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
  }

  .service-card-heading > div {
    display: grid;
    gap: 2px;
    min-width: 0;
  }

  .service-card-heading strong {
    font-size: 0.8125rem;
  }

  .service-card-heading span {
    color: var(--text-muted);
    font-size: 0.6875rem;
  }

  .state-badge {
    flex: 0 0 auto;
    padding: 4px 8px;
    color: var(--text-soft) !important;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--surface);
    font-weight: 620;
  }

  .state-badge.active {
    color: var(--success) !important;
    border-color: color-mix(in srgb, var(--success) 32%, var(--border));
  }

  .state-badge.probing {
    color: var(--primary) !important;
    border-color: color-mix(in srgb, var(--primary) 32%, var(--border));
  }

  .state-badge.cooldown {
    color: var(--danger) !important;
    border-color: color-mix(in srgb, var(--danger) 30%, var(--border));
  }

  .mode-options {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px;
    margin-top: 13px;
  }

  .mode-options button {
    display: grid;
    min-height: 76px;
    grid-template-columns: 28px minmax(0, 1fr);
    align-items: start;
    gap: 8px;
    padding: 10px;
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 11px;
    background: var(--surface);
    text-align: left;
  }

  .mode-options button:hover:not(:disabled) {
    border-color: var(--border-strong);
    background: var(--surface-hover);
  }

  .mode-options button.selected {
    border-color: color-mix(in srgb, var(--primary) 52%, var(--border));
    background: color-mix(in srgb, var(--primary-soft) 62%, var(--surface));
  }

  .mode-options button > span:last-child {
    display: grid;
    min-width: 0;
    gap: 3px;
  }

  .mode-options strong {
    font-size: 0.8125rem;
  }

  .mode-options small {
    color: var(--text-muted);
    font-size: 0.625rem;
    line-height: 1.45;
  }

  .mode-check {
    display: grid;
    width: 27px;
    height: 27px;
    place-items: center;
    color: var(--primary);
    border-radius: 8px;
    background: var(--primary-soft);
  }

  .hard-disabled,
  .service-notice,
  .account-error {
    display: flex;
    gap: 8px;
    margin-top: 11px;
    padding: 10px 11px;
    border-radius: 10px;
    font-size: 0.6875rem;
    line-height: 1.5;
  }

  .hard-disabled {
    color: var(--danger);
    border: 1px solid color-mix(in srgb, var(--danger) 25%, var(--border));
    background: color-mix(in srgb, var(--danger) 7%, var(--surface));
  }

  .service-save,
  .recheck-button {
    min-height: 44px;
    border-radius: 10px;
    font-size: 0.75rem;
    font-weight: 630;
  }

  .service-save {
    width: 100%;
    margin-top: 11px;
    color: var(--primary-contrast);
    border: 1px solid var(--primary);
    background: var(--primary);
  }

  .service-save:disabled {
    color: var(--text-muted);
    border-color: var(--border);
    background: var(--surface);
  }

  .recheck-button {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 6px;
    padding: 0 10px;
    color: var(--primary);
    border: 1px solid var(--border);
    background: var(--surface);
  }

  .service-boundary {
    margin: 9px 0 0;
    color: var(--text-muted);
    font-size: 0.625rem;
    line-height: 1.55;
  }

  .model-status-list {
    display: grid;
    gap: 6px;
    margin-top: 12px;
  }

  .model-status-list > div {
    display: flex;
    min-height: 48px;
    align-items: center;
    gap: 9px;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface);
  }

  .model-state {
    width: 8px;
    height: 8px;
    flex: 0 0 auto;
    border-radius: 50%;
    background: var(--text-faint);
  }

  .model-state.active {
    background: var(--success);
  }

  .model-state.probing {
    background: var(--primary);
  }

  .model-state.cooldown {
    background: var(--danger);
  }

  .model-status-copy {
    display: grid;
    min-width: 0;
    gap: 2px;
  }

  .model-status-copy strong {
    overflow: hidden;
    font-size: 0.75rem;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .model-status-copy small {
    color: var(--text-muted);
    font-size: 0.625rem;
  }

  .empty-status {
    margin-top: 12px;
    padding: 12px;
    color: var(--text-soft);
    border: 1px dashed var(--border-strong);
    border-radius: 10px;
    background: var(--surface);
    font-size: 0.6875rem;
    line-height: 1.55;
  }

  .service-notice {
    color: var(--success);
    border: 1px solid color-mix(in srgb, var(--success) 26%, var(--border));
    background: color-mix(in srgb, var(--success) 7%, var(--surface));
  }

  .account-error {
    color: var(--danger);
    border: 1px solid color-mix(in srgb, var(--danger) 25%, var(--border));
    background: color-mix(in srgb, var(--danger) 7%, var(--surface));
  }

  @media (max-width: 520px) {
    .mode-options {
      grid-template-columns: 1fr;
    }

    .service-card-heading {
      align-items: stretch;
      flex-direction: column;
    }

    .recheck-button {
      width: 100%;
      justify-content: center;
    }
  }
</style>
