<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';

  export let locale: Locale = 'zh-CN';
  export let current = false;
  export let disabled = false;

  const dispatch = createEventDispatcher<{
    retry: void;
    bypass: void;
  }>();
  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
</script>

<section class="guidance-error" role="alert">
  <span class="error-icon" aria-hidden="true"><Icon name="alert" size={16} /></span>
  <div>
    <strong>{t('交互卡片未能安全生成', 'The interactive card could not be generated safely')}</strong>
    <p>
      {t(
        '模型返回的卡片格式不符合限制，未展示或保存其中的操作内容。',
        'The model returned an invalid card format, so none of its actions were shown or saved.'
      )}
    </p>
    {#if current}
      <div class="error-actions">
        <button type="button" disabled={disabled} on:click={() => dispatch('retry')}>
          <Icon name="retry" size={15} />
          {t('重试生成卡片', 'Retry card')}
        </button>
        <button
          type="button"
          class="primary"
          disabled={disabled}
          on:click={() => dispatch('bypass')}
        >
          <Icon name="send" size={15} />
          {t('按原问题直接回答', 'Answer the original request')}
        </button>
      </div>
    {/if}
  </div>
</section>

<style>
  .guidance-error {
    display: flex;
    width: min(100%, 760px);
    gap: 10px;
    margin: 8px 0 14px;
    padding: 14px;
    color: var(--text);
    border: 1px solid color-mix(in srgb, var(--danger) 35%, var(--border));
    border-radius: 14px;
    background: color-mix(in srgb, var(--danger) 6%, var(--surface));
  }

  .error-icon {
    display: grid;
    width: 30px;
    height: 30px;
    flex: 0 0 30px;
    place-items: center;
    color: var(--danger);
    border-radius: 9px;
    background: color-mix(in srgb, var(--danger) 10%, var(--surface));
  }

  strong {
    font-size: 0.94rem;
  }

  p {
    margin: 4px 0 0;
    color: var(--text-soft);
    font-size: 0.84rem;
  }

  .error-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 12px;
  }

  button {
    display: inline-flex;
    min-height: 44px;
    gap: 7px;
    align-items: center;
    padding: 9px 13px;
    color: var(--text);
    border: 1px solid var(--border-strong);
    border-radius: 10px;
    background: var(--surface);
    font-weight: 650;
    cursor: pointer;
  }

  button.primary {
    color: var(--primary-contrast);
    border-color: var(--primary);
    background: var(--primary);
  }

  button:disabled {
    cursor: not-allowed;
    opacity: 0.5;
  }

  @media (max-width: 520px) {
    .error-actions {
      display: grid;
    }

    button {
      width: 100%;
      justify-content: center;
    }
  }
</style>
