<script lang="ts">
  import { createEventDispatcher, onMount } from 'svelte';
  import {
    getSpeechServiceSettings,
    updateSpeechServiceSettings
  } from './api';
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';
  import type { SpeechServiceSettings } from './types';

  export let locale: Locale = 'zh-CN';
  const dispatch = createEventDispatcher<{ changed: void }>();
  let settings: SpeechServiceSettings | null = null;
  let enabled = false;
  let provider = '';
  let defaultVoice = '';
  let loading = true;
  let pending = false;
  let error = '';
  let saved = false;

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: selectedProvider = settings?.providers.find((item) => item.id === provider);
  $: voices = selectedProvider?.voices || [];

  onMount(() => {
    void load();
  });

  async function load() {
    loading = true;
    error = '';
    try {
      settings = await getSpeechServiceSettings();
      enabled = settings.enabled;
      provider = settings.provider;
      defaultVoice = settings.defaultVoice;
    } catch (value) {
      error = value instanceof Error ? value.message : t('读取失败。', 'Could not load.');
    } finally {
      loading = false;
    }
  }

  function selectProvider(event: Event) {
    provider = (event.currentTarget as HTMLSelectElement).value;
    const descriptor = settings?.providers.find((item) => item.id === provider);
    if (!descriptor?.voices.some((item) => item.id === defaultVoice)) {
      defaultVoice = descriptor?.voices[0]?.id || '';
    }
    saved = false;
  }

  async function save() {
    if (!provider || !defaultVoice) return;
    pending = true;
    error = '';
    saved = false;
    try {
      settings = await updateSpeechServiceSettings(enabled, provider, defaultVoice);
      enabled = settings.enabled;
      provider = settings.provider;
      defaultVoice = settings.defaultVoice;
      saved = true;
      dispatch('changed');
    } catch (value) {
      error = value instanceof Error ? value.message : t('保存失败。', 'Could not save.');
    } finally {
      pending = false;
    }
  }

  function providerLabel(value: string): string {
    if (value === 'volcengine') return t('火山引擎', 'Volcengine');
    if (value === 'aliyun') return t('阿里云', 'Alibaba Cloud');
    return value;
  }
</script>

<div class="dialog-icon"><Icon name="speaker" size={23} /></div>
<h2 id="dialog-title">{t('语音服务设置', 'Speech service settings')}</h2>
<p class="dialog-lead">
  {t(
    '运行时切换提供商、默认音色和全局开关，不需要重启 CPA 或应用。',
    'Change the provider, default voice, and global switch at runtime without restarting CPA or the app.'
  )}
</p>

{#if loading}
  <div class="speech-settings-loading" role="status">
    <span class="tool-spinner"></span>{t('正在读取服务状态…', 'Loading service status…')}
  </div>
{:else if settings}
  <section class="speech-admin-status">
    <div>
      <span class:enabled class="speech-service-dot"></span>
      <span>
        <strong>{enabled ? t('语音服务已启用', 'Speech service enabled') : t('语音服务已停用', 'Speech service disabled')}</strong>
        <small>
          {t('每用户', 'Per user')} {settings.concurrency.perUser}
          · {t('全应用', 'Global')} {settings.concurrency.global}
          {t(' 个并发会话', ' concurrent sessions')}
        </small>
      </span>
    </div>
    <button
      class:enabled
      class="speech-switch"
      type="button"
      role="switch"
      aria-label={t('全局语音服务', 'Global speech service')}
      aria-checked={enabled}
      on:click={() => {
        enabled = !enabled;
        saved = false;
      }}
    >
      <span></span><b>{enabled ? t('已启用', 'On') : t('已停用', 'Off')}</b>
    </button>
  </section>

  <div class="speech-admin-form">
    <label>
      <span>{t('语音提供商', 'Speech provider')}</span>
      <select value={provider} disabled={pending} on:change={selectProvider}>
        {#each settings.providers as item}
          <option value={item.id}>
            {providerLabel(item.id)}
            · {item.configured ? t('已配置', 'configured') : t('未配置', 'not configured')}
          </option>
        {/each}
      </select>
    </label>

    <label>
      <span>{t('默认音色', 'Default voice')}</span>
      <select bind:value={defaultVoice} disabled={pending || voices.length === 0}>
        {#each voices as item}
          <option value={item.id}>{item.label}</option>
        {/each}
      </select>
    </label>
  </div>

  <div
    class:configured={Boolean(selectedProvider?.configured)}
    class="speech-provider-card"
    role="status"
  >
    <span><Icon name={selectedProvider?.configured ? 'check' : 'alert'} size={17} /></span>
    <span>
      <strong>{providerLabel(provider)}</strong>
      <small>
        {selectedProvider?.configured
          ? t('API 凭据已通过安全文件配置', 'API credentials are configured through a secure file')
          : t('尚未配置 API 凭据，启用会被服务器拒绝', 'API credentials are missing; the server will reject enabling')}
      </small>
    </span>
  </div>

  {#if error}
    <div class="speech-settings-notice danger" role="alert">
      <Icon name="alert" size={16} />{error}
    </div>
  {:else if saved}
    <div class="speech-settings-notice success" role="status">
      <Icon name="check" size={16} />{t('设置已即时生效。', 'Settings are now active.')}
    </div>
  {/if}

  <button
    class="dialog-primary"
    type="button"
    disabled={pending || (enabled && !selectedProvider?.configured) || !defaultVoice}
    on:click={save}
  >
    {pending ? t('正在保存…', 'Saving…') : t('保存语音服务设置', 'Save speech settings')}
  </button>
{:else}
  <div class="speech-settings-notice danger" role="alert">
    <Icon name="alert" size={16} />{error || t('语音服务状态不可用。', 'Speech service status is unavailable.')}
  </div>
{/if}
