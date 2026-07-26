<script lang="ts">
  import { createEventDispatcher, onMount, tick } from 'svelte';
  import Icon from './Icon.svelte';
  import { translate, type Locale } from './i18n';

  export let locale: Locale = 'zh-CN';

  const dispatch = createEventDispatcher<{ dismiss: void }>();
  let dialogElement: HTMLDivElement;

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);

  onMount(async () => {
    await tick();
    dialogElement?.focus();
  });

  function close() {
    dispatch('dismiss');
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

<div class="update-layer">
  <button
    type="button"
    class="update-backdrop"
    aria-label={t('关闭更新公告', 'Close update announcement')}
    on:click={close}
  ></button>
  <div
    class="update-dialog"
    bind:this={dialogElement}
    role="dialog"
    aria-modal="true"
    aria-labelledby="update-title"
    aria-describedby="update-description"
    tabindex="-1"
    on:keydown={handleKeydown}
  >
    <header>
      <div class="release-mark">
        <span><Icon name="speaker" size={19} /></span>
        <div>
          <strong>{t('更新公告', 'What’s new')}</strong>
          <small>La4RainGPT · 2026.07</small>
        </div>
      </div>
      <button
        type="button"
        class="close-button"
        aria-label={t('关闭', 'Close')}
        on:click={close}
      ><Icon name="close" size={19} /></button>
    </header>

    <section class="update-content">
      <div class="release-copy">
        <span class="release-tag">{t('新功能', 'New feature')}</span>
        <h2 id="update-title">{t('让回答为你读出来', 'Listen to answers as they arrive')}</h2>
        <p id="update-description">
          {t(
            '语音朗读已经上线。你可以手动朗读任意回答，也可以开启自动朗读并选择喜欢的音色。',
            'Read aloud is now available. Play any answer manually, or turn on automatic reading and choose a voice you like.'
          )}
        </p>
      </div>

      <div class="feature-list">
        <article>
          <span class="feature-icon"><Icon name="speaker" size={18} /></span>
          <div>
            <strong>{t('开启自动朗读', 'Turn on automatic reading')}</strong>
            <p>
              {t(
                '打开头像菜单，进入“语音与朗读”，选择“自动朗读”。首次开启时请按提示允许本设备播放声音。',
                'Open the profile menu, choose “Speech & read aloud,” then select “Automatic.” The first use asks this device for audio permission.'
              )}
            </p>
            <span class="location">
              {t('头像菜单', 'Profile')}
              <i aria-hidden="true">›</i>
              {t('语音与朗读', 'Speech & read aloud')}
              <i aria-hidden="true">›</i>
              {t('自动朗读', 'Automatic')}
            </span>
          </div>
        </article>

        <article>
          <span class="feature-icon"><Icon name="sparkles" size={18} /></span>
          <div>
            <strong>{t('选择音色与语速', 'Choose a voice and speed')}</strong>
            <p>
              {t(
                '同一设置页可以试听和切换音色，并调整默认语速；新设置会用于下一次朗读。',
                'Preview and switch voices on the same settings page, and adjust the default speed for your next reading.'
              )}
            </p>
            <span class="location">
              {t('语音与朗读', 'Speech & read aloud')}
              <i aria-hidden="true">›</i>
              {t('默认音色与语速', 'Voice & speed')}
            </span>
          </div>
        </article>

        <article>
          <span class="feature-icon"><Icon name="play" size={17} /></span>
          <div>
            <strong>{t('随时手动朗读', 'Read any answer manually')}</strong>
            <p>
              {t(
                '每条 Agent 回答下方都有“朗读”按钮。播放后可以暂停、快退或快进、拖动进度、调整倍速和音量。',
                'Every Agent answer has a “Read aloud” button below it, with pause, skip, seek, speed, and volume controls.'
              )}
            </p>
            <span class="location">
              {t('Agent 回答下方', 'Below an Agent answer')}
              <i aria-hidden="true">›</i>
              {t('朗读', 'Read aloud')}
            </span>
          </div>
        </article>
      </div>

      <aside>
        <Icon name="info" size={17} />
        <span>
          {t(
            '网页链接会继续显示在回答中，但朗读时会自动跳过网址，只读链接标题和正文。',
            'Web links remain visible in answers, while read aloud skips raw addresses and speaks only link titles and answer text.'
          )}
        </span>
      </aside>
    </section>

    <footer>
      <small>{t('以后可从头像菜单的“更新公告”重新查看', 'Reopen this from “What’s new” in the profile menu')}</small>
      <button type="button" on:click={close}>
        <Icon name="check" size={17} />{t('知道了', 'Got it')}
      </button>
    </footer>
  </div>
</div>

<style>
  .update-layer {
    position: fixed;
    z-index: 100;
    display: grid;
    padding: 24px;
    inset: 0;
    place-items: center;
  }

  .update-backdrop {
    position: absolute;
    padding: 0;
    cursor: pointer;
    border: 0;
    background: rgba(15, 20, 27, 0.52);
    inset: 0;
    backdrop-filter: blur(3px);
  }

  .update-dialog {
    position: relative;
    z-index: 1;
    width: min(100%, 620px);
    max-height: min(780px, calc(100dvh - 48px));
    overflow: auto;
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 22px;
    outline: none;
    background: var(--surface);
    box-shadow: var(--shadow-lg);
    overscroll-behavior: contain;
  }

  .update-dialog:focus-visible {
    box-shadow: var(--shadow-lg), 0 0 0 3px var(--focus);
  }

  header,
  footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 18px 22px;
  }

  header {
    border-bottom: 1px solid var(--border);
  }

  .release-mark {
    display: flex;
    min-width: 0;
    align-items: center;
    gap: 11px;
  }

  .release-mark > span,
  .feature-icon {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    color: var(--primary);
    border: 1px solid color-mix(in srgb, var(--primary) 20%, var(--border));
    background: var(--primary-soft);
  }

  .release-mark > span {
    width: 40px;
    height: 40px;
    border-radius: 12px;
  }

  .release-mark > div {
    display: grid;
    min-width: 0;
    gap: 1px;
  }

  .release-mark strong {
    font-size: 0.875rem;
  }

  .release-mark small {
    overflow: hidden;
    color: var(--text-muted);
    font-size: 0.6875rem;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  button {
    min-height: 44px;
    cursor: pointer;
    border: 0;
    font: inherit;
    touch-action: manipulation;
    transition:
      color 180ms ease,
      background 180ms ease,
      opacity 180ms ease,
      transform 150ms ease;
  }

  button:focus-visible {
    outline: 3px solid var(--focus);
    outline-offset: 2px;
  }

  .close-button {
    display: grid;
    width: 44px;
    flex: 0 0 auto;
    padding: 0;
    color: var(--text-muted);
    border-radius: 11px;
    background: transparent;
    place-items: center;
  }

  .close-button:hover {
    color: var(--text);
    background: var(--surface-hover);
  }

  .update-content {
    padding: 24px 22px;
  }

  .release-copy {
    max-width: 520px;
  }

  .release-tag {
    display: inline-flex;
    min-height: 26px;
    align-items: center;
    padding: 0 9px;
    color: var(--primary);
    border: 1px solid color-mix(in srgb, var(--primary) 22%, var(--border));
    border-radius: 999px;
    background: var(--primary-soft);
    font-size: 0.6875rem;
    font-weight: 700;
  }

  h2 {
    margin: 12px 0 8px;
    font-size: clamp(1.5rem, 4vw, 1.875rem);
    letter-spacing: -0.035em;
    line-height: 1.25;
  }

  .release-copy p {
    margin: 0;
    color: var(--text-soft);
    font-size: 0.875rem;
    line-height: 1.7;
  }

  .feature-list {
    display: grid;
    gap: 10px;
    margin-top: 22px;
  }

  article {
    display: grid;
    grid-template-columns: 42px minmax(0, 1fr);
    gap: 13px;
    padding: 15px;
    border: 1px solid var(--border);
    border-radius: 15px;
    background: var(--canvas);
  }

  .feature-icon {
    width: 42px;
    height: 42px;
    border-radius: 12px;
  }

  article > div {
    min-width: 0;
  }

  article strong {
    display: block;
    font-size: 0.875rem;
    line-height: 1.45;
  }

  article p {
    margin: 4px 0 9px;
    color: var(--text-soft);
    font-size: 0.75rem;
    line-height: 1.65;
  }

  .location {
    display: flex;
    max-width: 100%;
    flex-wrap: wrap;
    align-items: center;
    gap: 5px;
    color: var(--primary);
    font-size: 0.6875rem;
    font-weight: 650;
    line-height: 1.5;
  }

  .location i {
    color: var(--text-muted);
    font-style: normal;
  }

  aside {
    display: grid;
    grid-template-columns: 20px minmax(0, 1fr);
    gap: 9px;
    margin-top: 14px;
    padding: 12px 14px;
    color: var(--text-soft);
    border: 1px solid var(--border);
    border-radius: 13px;
    background: var(--surface-soft);
    font-size: 0.75rem;
    line-height: 1.6;
  }

  aside :global(.app-icon) {
    margin-top: 1px;
    color: var(--primary);
  }

  footer {
    border-top: 1px solid var(--border);
  }

  footer small {
    color: var(--text-muted);
    font-size: 0.6875rem;
    line-height: 1.5;
  }

  footer button {
    display: inline-flex;
    min-width: 108px;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 0 16px;
    color: var(--primary-contrast);
    border-radius: 11px;
    background: var(--primary);
    font-size: 0.8125rem;
    font-weight: 700;
  }

  footer button:hover {
    background: var(--primary-hover);
  }

  button:active {
    transform: scale(0.97);
  }

  @media (max-width: 620px) {
    .update-layer {
      align-items: end;
      padding: 0;
    }

    .update-dialog {
      width: 100%;
      max-height: min(92dvh, 780px);
      border-right: 0;
      border-bottom: 0;
      border-left: 0;
      border-radius: 22px 22px 0 0;
    }

    header {
      padding: 14px 16px;
    }

    .update-content {
      padding: 20px 16px;
    }

    .feature-list {
      margin-top: 18px;
    }

    article {
      grid-template-columns: 38px minmax(0, 1fr);
      gap: 11px;
      padding: 13px;
    }

    .feature-icon {
      width: 38px;
      height: 38px;
    }

    footer {
      align-items: center;
      padding: 13px 16px max(13px, env(safe-area-inset-bottom));
    }

    footer small {
      max-width: 210px;
    }
  }

  @media (max-width: 380px) {
    .update-content {
      padding-top: 16px;
    }

    h2 {
      font-size: 1.375rem;
    }

    footer small {
      display: none;
    }

    footer button {
      width: 100%;
    }
  }

  @media (max-height: 540px) and (orientation: landscape) {
    .update-dialog {
      max-height: 100dvh;
      border-radius: 0;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    button {
      transition: none;
    }
  }
</style>
