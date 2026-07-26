<script lang="ts">
  import type { Message } from './types';
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';
  import {
    messageSpeechText,
    speechController,
    speechPlayerState,
    speechPreference
  } from './speech';

  export let message: Message;
  export let locale: Locale = 'zh-CN';

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: hasText = Boolean(messageSpeechText(message));
  $: active = $speechPlayerState.messageId === message.id;
  $: available = Boolean(
    $speechPreference?.serviceEnabled && $speechPreference?.providerConfigured
  );
  $: isPlaying =
    active &&
    ($speechPlayerState.status === 'playing' ||
      $speechPlayerState.status === 'buffering' ||
      $speechPlayerState.status === 'connecting');
  $: isPaused = active && $speechPlayerState.status === 'paused';
  $: label = !available
    ? t('朗读服务暂不可用', 'Read aloud is unavailable')
    : isPlaying
      ? t('暂停朗读', 'Pause read aloud')
      : isPaused
        ? t('继续朗读', 'Resume read aloud')
        : active && $speechPlayerState.status === 'completed'
          ? t('重新朗读', 'Read again')
          : message.status === 'streaming'
            ? t('同步朗读', 'Read as it arrives')
            : t('朗读', 'Read aloud');

  async function toggle() {
    if (!available || !hasText) return;
    await speechController.playMessage(message);
  }
</script>

{#if hasText}
  <button
    class:active
    class="speech-message-control copy-answer"
    type="button"
    disabled={!available}
    aria-label={label}
    aria-pressed={active && (isPlaying || isPaused)}
    title={label}
    on:click={toggle}
  >
    <Icon name={isPlaying ? 'pause' : 'speaker'} size={15} />
    <span>{label}</span>
  </button>
{/if}
