<script lang="ts">
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';
  import {
    saveSpeechPreference,
    speechController,
    speechPlayerState,
    speechPreference
  } from './speech';
  import type { SpeechPlayerStatus } from './speech';

  export let locale: Locale = 'zh-CN';
  let seeking = false;
  let seekPreview = 0;
  let speedPending = false;
  const speeds = [0.5, 0.75, 1, 1.25, 1.5, 2];

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: visible = Boolean($speechPlayerState.messageId);
  $: shownTime = seeking ? seekPreview : $speechPlayerState.currentTime;
  $: playing =
    $speechPlayerState.status === 'playing' ||
    $speechPlayerState.status === 'buffering' ||
    $speechPlayerState.status === 'connecting';
  $: statusLabel = playerStatusLabel($speechPlayerState.status);

  function formatTime(seconds: number): string {
    const safe = Number.isFinite(seconds) ? Math.max(0, Math.floor(seconds)) : 0;
    const minutes = Math.floor(safe / 60);
    const remainder = safe % 60;
    return `${minutes}:${String(remainder).padStart(2, '0')}`;
  }

  function playerStatusLabel(status: SpeechPlayerStatus): string {
    if (status === 'connecting') return t('正在连接语音服务', 'Connecting to speech');
    if (status === 'buffering') return t('正在合成并缓冲', 'Synthesizing and buffering');
    if (status === 'playing') {
      return $speechPlayerState.streaming
        ? t('正在边生成边朗读', 'Reading as the answer arrives')
        : t('正在朗读', 'Reading aloud');
    }
    if (status === 'paused') return t('已暂停', 'Paused');
    if (status === 'completed') return t('朗读完成', 'Read aloud complete');
    if (status === 'error') return t('朗读失败', 'Read aloud failed');
    return '';
  }

  async function togglePlayback() {
    if ($speechPlayerState.status === 'paused') {
      await speechController.resume();
    } else if ($speechPlayerState.status === 'completed') {
      await speechController.replay();
    } else {
      await speechController.pause();
    }
  }

  function previewSeek(event: Event) {
    seeking = true;
    seekPreview = Number((event.currentTarget as HTMLInputElement).value);
  }

  function commitSeek(event: Event) {
    seekPreview = Number((event.currentTarget as HTMLInputElement).value);
    speechController.seek(seekPreview);
    seeking = false;
  }

  async function changeSpeed(event: Event) {
    const speed = Number((event.currentTarget as HTMLSelectElement).value);
    speechController.setSpeed(speed);
    if (!$speechPreference) return;
    speedPending = true;
    try {
      await saveSpeechPreference({ speed });
    } catch {
      // Playback speed still applies locally; the next settings save can retry persistence.
    } finally {
      speedPending = false;
    }
  }

  function changeVolume(event: Event) {
    speechController.setVolume(Number((event.currentTarget as HTMLInputElement).value));
  }
</script>

{#if visible}
  <section
    class:error={$speechPlayerState.status === 'error'}
    class="speech-player"
    aria-label={t('朗读播放器', 'Read-aloud player')}
  >
    <div class="speech-player-main">
      <div class="speech-player-identity">
        <span class="speech-player-glyph" aria-hidden="true">
          <Icon name="speaker" size={18} />
        </span>
        <span class="speech-player-copy">
          <strong role="status" aria-live="polite">{statusLabel}</strong>
          <small title={$speechPlayerState.label}>{$speechPlayerState.label}</small>
        </span>
      </div>

      <div class="speech-player-controls">
        <button
          type="button"
          aria-label={t('后退 10 秒', 'Back 10 seconds')}
          title={t('后退 10 秒', 'Back 10 seconds')}
          disabled={$speechPlayerState.duration <= 0}
          on:click={() => speechController.skip(-10)}
        ><Icon name="rewind" size={18} /></button>
        <button
          class="speech-play-button"
          type="button"
          aria-label={playing ? t('暂停', 'Pause') : t('播放', 'Play')}
          title={playing ? t('暂停', 'Pause') : t('播放', 'Play')}
          disabled={$speechPlayerState.status === 'connecting' || $speechPlayerState.status === 'error'}
          on:click={togglePlayback}
        >
          <Icon name={playing ? 'pause' : 'play'} size={20} />
        </button>
        <button
          type="button"
          aria-label={t('前进 10 秒', 'Forward 10 seconds')}
          title={t('前进 10 秒', 'Forward 10 seconds')}
          disabled={$speechPlayerState.duration <= 0}
          on:click={() => speechController.skip(10)}
        ><Icon name="forward" size={18} /></button>
        <button
          type="button"
          aria-label={t('停止并关闭朗读', 'Stop and close read aloud')}
          title={t('停止并关闭朗读', 'Stop and close read aloud')}
          on:click={() => speechController.stop()}
        ><Icon name="close" size={18} /></button>
      </div>
    </div>

    {#if $speechPlayerState.status === 'error'}
      <div class="speech-player-error" role="alert">
        <Icon name="alert" size={15} />
        {$speechPlayerState.errorMessage}
      </div>
    {:else}
      <div class="speech-player-timeline">
        <span>{formatTime(shownTime)}</span>
        <input
          type="range"
          min="0"
          max={Math.max($speechPlayerState.buffered, 0.01)}
          step="0.05"
          value={shownTime}
          disabled={$speechPlayerState.buffered <= 0}
          aria-label={t('朗读进度', 'Read-aloud progress')}
          aria-valuetext={`${formatTime(shownTime)} / ${formatTime($speechPlayerState.duration)}`}
          on:input={previewSeek}
          on:change={commitSeek}
          on:pointerup={commitSeek}
          on:keyup={commitSeek}
        />
        <span>{formatTime($speechPlayerState.duration)}</span>
      </div>

      <div class="speech-player-options">
        <label class="speech-speed">
          <span>{t('倍速', 'Speed')}</span>
          <select
            aria-label={t('朗读倍速', 'Read-aloud speed')}
            value={$speechPlayerState.speed}
            disabled={speedPending}
            on:change={changeSpeed}
          >
            {#each speeds as speed}
              <option value={speed}>{speed}×</option>
            {/each}
          </select>
        </label>
        <label class="speech-volume">
          <Icon
            name={$speechPlayerState.volume === 0 ? 'volume-off' : 'speaker'}
            size={16}
          />
          <span class="sr-only">{t('音量', 'Volume')}</span>
          <input
            type="range"
            min="0"
            max="1"
            step="0.05"
            value={$speechPlayerState.volume}
            aria-label={t('朗读音量', 'Read-aloud volume')}
            on:input={changeVolume}
          />
        </label>
        <span class="speech-buffer-status" role="status">
          {#if $speechPlayerState.streaming}
            {t('已缓冲', 'Buffered')} {formatTime($speechPlayerState.buffered)}
          {:else}
            {$speechPlayerState.provider === 'volcengine'
              ? t('火山引擎', 'Volcengine')
              : $speechPlayerState.provider}
          {/if}
        </span>
      </div>
    {/if}
  </section>
{/if}
