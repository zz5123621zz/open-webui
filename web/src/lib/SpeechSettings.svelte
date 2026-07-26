<script lang="ts">
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';
  import type { Message, SpeechMode } from './types';
  import {
    saveSpeechPreference,
    speechController,
    speechDeviceAuthorization,
    speechPlayerState,
    speechPreference,
    speechPreferenceError,
    speechPreferenceLoading
  } from './speech';

  export let locale: Locale = 'zh-CN';
  let initialized = false;
  let mode: SpeechMode = 'manual';
  let speed = 1;
  let voice = '';
  let pending = false;
  let localError = '';
  const previewMessageId = 'speech-settings-preview';

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: if ($speechPreference && !initialized) {
    mode = $speechPreference.mode;
    speed = $speechPreference.speed;
    voice = $speechPreference.voice;
    initialized = true;
  }
  $: selectedVoice =
    voice || $speechPreference?.effectiveVoice || $speechPreference?.voices[0]?.id || '';
  $: selectedVoiceEnglish = selectedVoice.startsWith('en_');
  $: previewActive = $speechPlayerState.messageId === previewMessageId;
  $: previewPlaying =
    previewActive &&
    ($speechPlayerState.status === 'connecting' ||
      $speechPlayerState.status === 'buffering' ||
      $speechPlayerState.status === 'playing');

  async function persist(patch: { mode?: SpeechMode; speed?: number; voice?: string }) {
    pending = true;
    localError = '';
    try {
      const next = await saveSpeechPreference(patch);
      mode = next.mode;
      speed = next.speed;
      voice = next.voice;
    } catch (error) {
      localError = error instanceof Error ? error.message : t('保存失败。', 'Could not save.');
    } finally {
      pending = false;
    }
  }

  async function toggleAutoRead() {
    if (!$speechPreference) return;
    if (mode === 'auto') {
      await persist({ mode: 'manual' });
      return;
    }
    pending = true;
    localError = '';
    try {
      await speechController.authorize();
      await persist({ mode: 'auto' });
    } catch (error) {
      localError =
        error instanceof Error
          ? error.message
          : t('浏览器没有允许播放声音。', 'The browser did not allow audio playback.');
      pending = false;
    }
  }

  async function authorizeDevice() {
    pending = true;
    localError = '';
    try {
      await speechController.authorize();
    } catch (error) {
      localError =
        error instanceof Error
          ? error.message
          : t('浏览器没有允许播放声音。', 'The browser did not allow audio playback.');
    } finally {
      pending = false;
    }
  }

  async function changeSpeed(event: Event) {
    speed = Number((event.currentTarget as HTMLSelectElement).value);
    speechController.setSpeed(speed);
    await persist({ speed });
  }

  async function changeVoice(event: Event) {
    voice = (event.currentTarget as HTMLSelectElement).value;
    if (previewActive) speechController.stop();
    await persist({ voice });
  }

  async function previewVoice() {
    if (!$speechPreference) return;
    pending = true;
    localError = '';
    try {
      if (!$speechDeviceAuthorization.active) await speechController.authorize();
      if (voice !== $speechPreference.voice || speed !== $speechPreference.speed) {
        await persist({ voice, speed });
      }
      const preview: Message = {
        id: previewMessageId,
        conversationId: 'speech-preview',
        role: 'assistant',
        status: 'completed',
        createdAt: Date.now(),
        parts: [
          {
            type: 'text',
            text: selectedVoiceEnglish
              ? 'Hello, this is La4RainGPT. This is a preview of the selected voice.'
              : '你好，我是 La4RainGPT。这是当前音色的试听效果。'
          }
        ]
      };
      await speechController.playMessage(preview);
    } catch (error) {
      localError =
        error instanceof Error ? error.message : t('试听失败。', 'Preview failed.');
    } finally {
      pending = false;
    }
  }
</script>

<div class="dialog-icon"><Icon name="speaker" size={23} /></div>
<h2 id="dialog-title">{t('语音与朗读', 'Speech & read aloud')}</h2>
<p class="dialog-lead">
  {t(
    '手动朗读始终可用；自动朗读会在正文出现完整句子后开始，不会朗读推理或工具过程。',
    'Manual read-aloud is always available. Auto-read starts after a complete answer sentence and skips reasoning and tool activity.'
  )}
</p>

{#if $speechPreferenceLoading}
  <div class="speech-settings-loading" role="status">
    <span class="tool-spinner"></span>{t('正在读取语音设置…', 'Loading speech settings…')}
  </div>
{:else if !$speechPreference}
  <div class="speech-settings-notice danger" role="alert">
    <Icon name="alert" size={16} />
    {$speechPreferenceError || t('语音设置暂不可用。', 'Speech settings are unavailable.')}
  </div>
{:else}
  {#if !$speechPreference.serviceEnabled || !$speechPreference.providerConfigured}
    <div class="speech-settings-notice" role="status">
      <Icon name="info" size={16} />
      {t(
        '管理员尚未启用或配置语音服务，现有设置会被保留。',
        'The administrator has not enabled or configured speech. Your preferences will be retained.'
      )}
    </div>
  {/if}

  <section class="speech-setting-section">
    <div class="speech-setting-heading">
      <span>
        <strong>{t('自动朗读', 'Automatic read-aloud')}</strong>
        <small>
          {t(
            '默认关闭；开启后只朗读最新回答正文',
            'Off by default; reads only the newest assistant answer'
          )}
        </small>
      </span>
      <button
        class:enabled={mode === 'auto'}
        class="speech-switch"
        type="button"
        role="switch"
        aria-label={t('自动朗读', 'Automatic read-aloud')}
        aria-checked={mode === 'auto'}
        disabled={pending || !$speechPreference.serviceEnabled}
        on:click={toggleAutoRead}
      >
        <span></span>
        <b>{mode === 'auto' ? t('已开启', 'On') : t('已关闭', 'Off')}</b>
      </button>
    </div>
  </section>

  <section class="speech-setting-section">
    <label class="speech-setting-field">
      <span>
        <strong>{t('默认音色', 'Default voice')}</strong>
        <small>
          {selectedVoiceEnglish
            ? t('英文音色仅建议朗读英文内容', 'English voice; use it for English content')
            : t('应用于下一次朗读', 'Used for the next read-aloud session')}
        </small>
      </span>
      <select value={selectedVoice} disabled={pending} on:change={changeVoice}>
        {#each $speechPreference.voices as item}
          <option value={item.id}>{item.label}</option>
        {/each}
      </select>
    </label>

    <label class="speech-setting-field">
      <span>
        <strong>{t('朗读速度', 'Reading speed')}</strong>
        <small>{t('播放中也可以随时调整', 'Can also be changed during playback')}</small>
      </span>
      <select value={speed} disabled={pending} on:change={changeSpeed}>
        {#each [0.5, 0.75, 1, 1.25, 1.5, 2] as item}
          <option value={item}>{item}×</option>
        {/each}
      </select>
    </label>
  </section>

  <section class="speech-device-card">
    <span class:active={$speechDeviceAuthorization.active} class="speech-device-status">
      <Icon name={$speechDeviceAuthorization.active ? 'check' : 'speaker'} size={16} />
    </span>
    <span>
      <strong>
        {$speechDeviceAuthorization.active
          ? t('当前设备已允许播放', 'Audio is enabled on this device')
          : t('当前设备需要声音授权', 'This device needs audio permission')}
      </strong>
      <small>
        {t(
          '浏览器要求每台设备通过一次用户点击解锁声音。',
          'Browsers require a user gesture to unlock audio on each device.'
        )}
      </small>
    </span>
    {#if !$speechDeviceAuthorization.active}
      <button type="button" disabled={pending} on:click={authorizeDevice}>
        {t('允许播放声音', 'Enable audio')}
      </button>
    {/if}
  </section>

  {#if localError}
    <div class="speech-settings-notice danger" role="alert">
      <Icon name="alert" size={16} />{localError}
    </div>
  {/if}

  <button
    class="dialog-primary speech-preview-button"
    type="button"
    disabled={pending ||
      !$speechPreference.serviceEnabled ||
      !$speechPreference.providerConfigured ||
      $speechPlayerState.status === 'connecting'}
    on:click={previewVoice}
  >
    <Icon name={previewPlaying ? 'pause' : 'play'} size={17} />
    {pending
      ? t('正在准备…', 'Preparing…')
      : previewPlaying
        ? t('暂停试听', 'Pause preview')
        : previewActive && $speechPlayerState.status === 'paused'
          ? t('继续试听', 'Resume preview')
          : t('试听当前音色', 'Preview selected voice')}
  </button>

  <p class="appearance-note">
    {t(
      '语音由已配置的第三方提供商生成。音频只缓存在当前浏览器内存中，停止或退出后释放。',
      'Speech is generated by the configured provider. Audio is buffered only in browser memory and released when stopped or signed out.'
    )}
  </p>
{/if}
