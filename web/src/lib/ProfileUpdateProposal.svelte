<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import type { ProfileUpdateProposal } from './types';
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';

  export let proposal: ProfileUpdateProposal;
  export let locale: Locale = 'zh-CN';
  export let value: 'save' | 'current_task_only' | 'ignore' = 'current_task_only';
  export let disabled = false;

  const dispatch = createEventDispatcher<{
    change: { value: 'save' | 'current_task_only' | 'ignore' };
  }>();

  const fieldLabels: Record<string, [string, string]> = {
    city_area: ['城市或商圈', 'City or business area'],
    cuisine_positioning: ['菜系与定位', 'Cuisine and positioning'],
    average_spend: ['大致客单价', 'Typical spend per guest'],
    primary_customers: ['主要顾客', 'Primary customers'],
    venue_scale: ['门店规模', 'Venue scale'],
    kitchen_scale: ['后厨规模', 'Kitchen scale'],
    consumption_scenarios: ['常见消费场景', 'Common dining occasions'],
    equipment_constraints: ['稳定设备限制', 'Stable equipment constraints']
  };

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: fieldLabel = fieldLabels[proposal.field]
    ? t(...fieldLabels[proposal.field])
    : proposal.field;

  function choose(next: 'save' | 'current_task_only' | 'ignore') {
    if (disabled) return;
    value = next;
    dispatch('change', { value: next });
  }
</script>

<section class="profile-proposal" aria-label={t('餐厅档案更新提议', 'Restaurant profile update proposal')}>
  <header>
    <span><Icon name="info" size={15} /></span>
    <div>
      <strong>{t('是否顺便记住这条稳定信息？', 'Remember this stable detail?')}</strong>
      <small>{t('默认只用于本次任务，不增加日常维护负担。', 'The default applies only to this task.')}</small>
    </div>
  </header>
  <div class="proposal-value">
    <b>{fieldLabel}</b>
    {#if proposal.operation === 'delete'}
      <span>{t('删除现有档案值', 'Remove the saved value')}</span>
    {:else}
      <span>{proposal.proposedValue}</span>
    {/if}
    <small>{proposal.reason}</small>
  </div>
  <div class="decision-options" role="radiogroup" aria-label={t('档案处理方式', 'Profile decision')}>
    <button
      type="button"
      role="radio"
      aria-checked={value === 'current_task_only'}
      class:selected={value === 'current_task_only'}
      {disabled}
      on:click={() => choose('current_task_only')}
    >
      <span class="radio-mark">{#if value === 'current_task_only'}<Icon name="check" size={13} />{/if}</span>
      <span>
        <strong>{t('只用于本次', 'This task only')}</strong>
        <small>{t('推荐，不改长期档案', 'Recommended; do not change the saved profile')}</small>
      </span>
    </button>
    <button
      type="button"
      role="radio"
      aria-checked={value === 'save'}
      class:selected={value === 'save'}
      {disabled}
      on:click={() => choose('save')}
    >
      <span class="radio-mark">{#if value === 'save'}<Icon name="check" size={13} />{/if}</span>
      <span>
        <strong>{t('保存到档案', 'Save to profile')}</strong>
        <small>{t('以后相关任务可直接使用', 'Reuse it in future relevant tasks')}</small>
      </span>
    </button>
    <button
      type="button"
      role="radio"
      aria-checked={value === 'ignore'}
      class:selected={value === 'ignore'}
      {disabled}
      on:click={() => choose('ignore')}
    >
      <span class="radio-mark">{#if value === 'ignore'}<Icon name="check" size={13} />{/if}</span>
      <span>
        <strong>{t('忽略提议', 'Ignore')}</strong>
        <small>{t('本次也不采用这条档案提议', 'Do not use the profile proposal')}</small>
      </span>
    </button>
  </div>
</section>

<style>
  .profile-proposal {
    margin-top: 14px;
    overflow: hidden;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface-soft);
  }

  header {
    display: flex;
    gap: 8px;
    align-items: flex-start;
    padding: 12px;
  }

  header > span {
    display: grid;
    width: 26px;
    height: 26px;
    flex: 0 0 26px;
    place-items: center;
    color: var(--primary);
    border-radius: 8px;
    background: var(--primary-soft);
  }

  header strong,
  header small,
  .proposal-value span,
  .proposal-value small,
  button strong,
  button small {
    display: block;
  }

  header strong {
    font-size: 0.9rem;
  }

  header small,
  .proposal-value small,
  button small {
    margin-top: 2px;
    color: var(--text-soft);
    font-size: 0.78rem;
    line-height: 1.4;
  }

  .proposal-value {
    margin: 0 12px 12px;
    padding: 10px 12px;
    overflow-wrap: anywhere;
    border-radius: 10px;
    background: var(--surface);
    font-size: 0.86rem;
  }

  .proposal-value b {
    color: var(--text-soft);
  }

  .proposal-value span {
    margin-top: 3px;
    font-weight: 650;
  }

  .decision-options {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
    padding: 0 12px 12px;
  }

  button {
    display: flex;
    min-height: 56px;
    min-width: 0;
    gap: 8px;
    align-items: flex-start;
    padding: 9px 10px;
    color: var(--text);
    text-align: left;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface);
    cursor: pointer;
  }

  button > span:last-child {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  button.selected {
    border-color: var(--primary);
    background: var(--primary-soft);
  }

  .radio-mark {
    display: grid;
    width: 19px;
    height: 19px;
    flex: 0 0 19px;
    place-items: center;
    color: var(--primary-contrast);
    border: 1px solid var(--border-strong);
    border-radius: 50%;
    background: var(--surface);
  }

  button.selected .radio-mark {
    border-color: var(--primary);
    background: var(--primary);
  }

  button strong {
    font-size: 0.82rem;
  }

  @media (max-width: 620px) {
    .decision-options {
      grid-template-columns: 1fr;
    }
  }
</style>
