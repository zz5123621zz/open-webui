<script lang="ts">
  import { createEventDispatcher, onMount } from 'svelte';
  import {
    getDictationServiceSettings,
    updateDictationServiceSettings
  } from './api';
  import Icon from './Icon.svelte';
  import { translate, type Locale } from './i18n';
  import type { DictationServiceSettings } from './types';

  export let locale: Locale = 'zh-CN';

  const dispatch = createEventDispatcher<{ changed: void }>();
  let settings: DictationServiceSettings | null = null;
  let enabled = false;
  let loading = true;
  let pending = false;
  let error = '';
  let saved = false;

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);

  onMount(() => {
    void load();
  });

  async function load() {
    loading = true;
    error = '';
    try {
      settings = await getDictationServiceSettings();
      enabled = settings.enabled;
    } catch (value) {
      error = value instanceof Error ? value.message : t('读取失败。', 'Could not load.');
    } finally {
      loading = false;
    }
  }

  async function save() {
    pending = true;
    error = '';
    saved = false;
    try {
      settings = await updateDictationServiceSettings(enabled);
      enabled = settings.enabled;
      saved = true;
      dispatch('changed');
    } catch (value) {
      error = value instanceof Error ? value.message : t('保存失败。', 'Could not save.');
    } finally {
      pending = false;
    }
  }
</script>

<div class="dialog-icon"><Icon name="microphone" size={23} /></div>
<h2 id="dialog-title">{t('语音输入设置', 'Voice input settings')}</h2>
<p class="dialog-lead">
  {t(
    '独立控制网页端语音转文字。关闭后只阻止新录音，不影响已开始的识别，也不会关闭回答朗读或微信 WAV。',
    'Control browser speech-to-text independently. Turning it off blocks only new recordings and does not disable read-aloud or WeChat WAV files.'
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
        <strong>
          {enabled
            ? t('语音输入已启用', 'Voice input enabled')
            : t('语音输入已停用', 'Voice input disabled')}
        </strong>
        <small>
          {t('每用户', 'Per user')} {settings.concurrency.perUser}
          · {t('全应用', 'Global')} {settings.concurrency.global}
          · {settings.maxDurationSeconds / 60}
          {t(' 分钟上限', ' minute limit')}
        </small>
      </span>
    </div>
    <button
      class:enabled
      class="speech-switch"
      type="button"
      role="switch"
      aria-label={t('全局语音输入', 'Global voice input')}
      aria-checked={enabled}
      disabled={pending}
      on:click={() => {
        enabled = !enabled;
        saved = false;
      }}
    >
      <span></span><b>{enabled ? t('已启用', 'On') : t('已停用', 'Off')}</b>
    </button>
  </section>

  <div
    class:configured={settings.configured}
    class="speech-provider-card"
    role="status"
  >
    <span><Icon name={settings.configured ? 'check' : 'alert'} size={17} /></span>
    <span>
      <strong>{t('豆包流式语音识别 2.0', 'Doubao streaming ASR 2.0')}</strong>
      <small>
        {settings.configured
          ? t(
              'API 凭据由独立安全文件提供；麦克风原始音频不保存',
              'Credentials use a separate secret file; raw microphone audio is not stored'
            )
          : t(
              '尚未配置独立 ASR 凭据，启用会被服务器拒绝',
              'The separate ASR credential is missing; enabling will be rejected'
            )}
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
    disabled={pending || (enabled && !settings.configured)}
    on:click={save}
  >
    {pending ? t('正在保存…', 'Saving…') : t('保存语音输入设置', 'Save voice input settings')}
  </button>
{:else}
  <div class="speech-settings-notice danger" role="alert">
    <Icon name="alert" size={16} />{error ||
      t('语音输入状态不可用。', 'Voice input status is unavailable.')}
  </div>
{/if}
