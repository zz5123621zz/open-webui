<script lang="ts">
  import { createEventDispatcher, onMount } from 'svelte';
  import {
    getConversationLimitSettings,
    updateConversationLimitSettings
  } from './api';
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';
  import type { ConversationLimitSettings } from './types';

  export let locale: Locale = 'zh-CN';
  const dispatch = createEventDispatcher<{ changed: void }>();

  let settings: ConversationLimitSettings | null = null;
  let unlimited = true;
  let maxActive = 30;
  let loading = true;
  let pending = false;
  let error = '';
  let saved = false;

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: dirty = Boolean(
    settings &&
      (unlimited !== settings.unlimited ||
        (!unlimited && maxActive !== settings.maxActiveConversations))
  );

  onMount(() => {
    void load();
  });

  async function load() {
    loading = true;
    error = '';
    try {
      apply(await getConversationLimitSettings());
    } catch (value) {
      error = value instanceof Error ? value.message : t('读取失败。', 'Could not load.');
    } finally {
      loading = false;
    }
  }

  function apply(next: ConversationLimitSettings) {
    settings = next;
    unlimited = next.unlimited;
    maxActive = next.unlimited
      ? next.envDefault > 0
        ? next.envDefault
        : 30
      : next.maxActiveConversations;
    saved = false;
  }

  async function save() {
    if (!dirty || pending) return;
    const nextValue = unlimited ? 0 : Math.trunc(maxActive);
    pending = true;
    error = '';
    saved = false;
    try {
      apply(await updateConversationLimitSettings(nextValue));
      saved = true;
      dispatch('changed');
    } catch (value) {
      error = value instanceof Error ? value.message : t('保存失败。', 'Could not save.');
    } finally {
      pending = false;
    }
  }
</script>

<div class="dialog-icon"><Icon name="chat" size={23} /></div>
<h2 id="dialog-title">{t('会话数量上限', 'Conversation limit')}</h2>
<p class="dialog-lead">
  {t(
    '设置每名用户可同时保留的活跃对话数量。0 或不限制表示不再自动把旧对话移入临时留档。',
    'Set how many active chats each user may keep. Unlimited (0) stops automatic retention of older chats.'
  )}
</p>

{#if loading}
  <div class="limit-loading" role="status">
    <span class="tool-spinner"></span>{t('正在读取服务设置…', 'Loading service settings…')}
  </div>
{:else if settings}
  <section class="limit-status">
    <div>
      <span class:unlimited class="limit-dot"></span>
      <span>
        <strong>
          {settings.unlimited
            ? t('当前不限制会话数量', 'No conversation limit')
            : t(`当前上限 ${settings.maxActiveConversations} 个`, `Current limit ${settings.maxActiveConversations}`)}
        </strong>
        <small>
          {settings.source === 'admin'
            ? t('管理员已覆盖部署默认值', 'Administrator override of the deployment default')
            : t(`部署默认 ${settings.envDefault || t('不限制', 'unlimited')}`, `Deployment default ${settings.envDefault || 'unlimited'}`)}
        </small>
      </span>
    </div>
  </section>

  <label class="limit-switch">
    <input
      type="checkbox"
      bind:checked={unlimited}
      disabled={pending}
      on:change={() => (saved = false)}
    />
    <span>{t('不限制每名用户的活跃对话数', 'Do not limit active chats per user')}</span>
  </label>

  <label class="limit-field">
    <span>{t('每名用户最多保留的活跃对话', 'Maximum active chats per user')}</span>
    <input
      type="number"
      min="1"
      max="10000"
      step="1"
      bind:value={maxActive}
      disabled={pending || unlimited}
      on:input={() => (saved = false)}
    />
    <small>{t('填写 1–10000；不限制时此项无效。', 'Enter 1–10000. Ignored when unlimited.')}</small>
  </label>

  {#if error}
    <div class="limit-notice danger" role="alert"><Icon name="alert" size={16} />{error}</div>
  {:else if saved}
    <div class="limit-notice success" role="status">
      <Icon name="check" size={16} />{t('设置已即时生效。', 'Settings are now active.')}
    </div>
  {/if}

  <button class="dialog-primary" type="button" disabled={!dirty || pending} on:click={save}>
    {pending ? t('正在保存…', 'Saving…') : t('保存会话上限', 'Save conversation limit')}
  </button>
  <p class="limit-note">
    {t(
      '只影响之后新建或恢复的对话，不会改写已经留档的记录。',
      'This only affects chats created or restored from now on. Already retained chats stay as they are.'
    )}
  </p>
{:else}
  <div class="limit-notice danger" role="alert">
    <Icon name="alert" size={16} />{error || t('会话上限设置不可用。', 'Conversation limit settings are unavailable.')}
  </div>
{/if}

<style>
  .limit-loading,
  .limit-status,
  .limit-switch,
  .limit-field,
  .limit-notice,
  .limit-note {
    display: flex;
    gap: 0.7rem;
  }
  .limit-loading {
    align-items: center;
    color: var(--muted);
    font-size: 0.92rem;
  }
  .limit-status {
    align-items: flex-start;
    margin: 0.2rem 0 1rem;
  }
  .limit-status > div {
    display: flex;
    gap: 0.7rem;
    align-items: flex-start;
  }
  .limit-status strong,
  .limit-status small {
    display: block;
  }
  .limit-status small {
    margin-top: 0.2rem;
    color: var(--muted);
  }
  .limit-dot {
    width: 0.7rem;
    height: 0.7rem;
    margin-top: 0.35rem;
    border-radius: 999px;
    background: #c2410c;
    flex: 0 0 auto;
  }
  .limit-dot.unlimited {
    background: #15803d;
  }
  .limit-switch,
  .limit-field {
    flex-direction: column;
    margin: 0 0 0.9rem;
    font-size: 0.92rem;
  }
  .limit-switch {
    flex-direction: row;
    align-items: center;
  }
  .limit-field input {
    width: 8rem;
    border: 1px solid var(--line, rgba(127, 127, 127, 0.35));
    border-radius: 0.55rem;
    padding: 0.45rem 0.6rem;
    background: var(--surface, transparent);
    color: inherit;
  }
  .limit-field small,
  .limit-note {
    color: var(--muted);
    font-size: 0.82rem;
    line-height: 1.45;
  }
  .limit-notice {
    align-items: flex-start;
    margin: 0 0 0.9rem;
    font-size: 0.9rem;
  }
  .limit-notice.danger {
    color: #b45309;
  }
  .limit-notice.success {
    color: #15803d;
  }
</style>
