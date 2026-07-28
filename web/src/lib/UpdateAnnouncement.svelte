<script lang="ts">
  import { createEventDispatcher, onMount, tick } from 'svelte';
  import Icon from './Icon.svelte';
  import { translate, type Locale } from './i18n';

  export let locale: Locale = 'zh-CN';
  export let restaurantExperience = false;

  type Copy = [string, string];
  type FeatureIcon = 'chat' | 'check' | 'edit' | 'image-plus' | 'info' | 'plan' | 'search';
  type ReleaseFeature = {
    icon: FeatureIcon;
    title: Copy;
    description: Copy;
    location: Copy[];
  };
  type Release = {
    tag: Copy;
    title: Copy;
    description: Copy;
    features: ReleaseFeature[];
    note: Copy;
  };

  const generalRelease: Release = {
    tag: ['功能更新', 'Feature update'],
    title: ['搜索、编辑与更顺手的输入', 'Search, editing, and a smoother composer'],
    description: [
      '这次更新带来对话搜索、消息编辑重发、粘贴或拖拽上传、草稿自动保存与用量统计，手机还能把应用添加到主屏幕。',
      'This update adds chat search, message editing, paste-or-drop uploads, automatic drafts, usage stats, and add-to-home-screen on mobile.'
    ],
    features: [
      {
        icon: 'search',
        title: ['搜索所有对话', 'Search every chat'],
        description: [
          '侧边栏新增搜索框，可按标题和消息内容查找对话，结果附带匹配片段，点击直接打开。',
          'A search box in the sidebar finds chats by title or message text, shows a matching snippet, and opens the chat with one tap.'
        ],
        location: [
          ['侧边栏', 'Sidebar'],
          ['搜索对话', 'Search chats']
        ]
      },
      {
        icon: 'edit',
        title: ['编辑并重新发送', 'Edit and resend'],
        description: [
          '最新一条你的消息下方有“编辑”按钮，改完点“重新发送”即可得到新回答；之前的回答仍会保留，方便对照。',
          'Your latest message now has an “Edit” button. Resend it to get a fresh answer while earlier answers stay in the chat for comparison.'
        ],
        location: [
          ['最新一条你的消息', 'Your latest message'],
          ['编辑', 'Edit']
        ]
      },
      {
        icon: 'image-plus',
        title: ['粘贴上传与自动草稿', 'Paste, drop, and drafts'],
        description: [
          '截图可以直接粘贴进输入框，图片也可以拖进聊天窗口上传；没发送的文字会按对话自动保存，刷新页面后自动恢复。',
          'Paste screenshots straight into the composer or drop images onto the chat. Unsent text is saved per chat and restored after a reload.'
        ],
        location: [['输入框', 'Composer']]
      },
      {
        icon: 'plan',
        title: ['用量统计与主屏幕', 'Usage stats and home screen'],
        description: [
          '头像菜单新增“用量统计”，按月查看回答次数与 token 消耗；手机浏览器现在可以把 La4RainGPT 添加到主屏幕全屏使用。',
          'The profile menu gains “Usage stats” with monthly response and token totals, and mobile browsers can add La4RainGPT to the home screen.'
        ],
        location: [
          ['头像菜单', 'Profile'],
          ['用量统计', 'Usage stats']
        ]
      }
    ],
    note: [
      '搜索覆盖你自己的全部活跃对话，临时留档中的对话暂不参与；草稿只保存在当前浏览器。',
      'Search covers all of your active chats; retained chats are not searched yet, and drafts stay in this browser only.'
    ]
  };

  const restaurantRelease: Release = {
    tag: ['餐饮定制', 'Restaurant edition'],
    title: ['餐饮问题，可以先说个大概', 'Start restaurant tasks with a rough idea'],
    description: [
      'La4RainGPT 已为餐饮经营任务增加引导式聊天。问题较宽泛时，Agent 会用少量可点击选项逐步补齐目标、背景和限制，再生成更贴合实际的回答。',
      'La4RainGPT now guides restaurant-management tasks. When a request is broad, the Agent uses a few tappable choices to clarify the goal, context, and constraints before producing a more relevant answer.'
    ],
    features: [
      {
        icon: 'chat',
        title: ['模糊问题先问关键点', 'Clarify only what matters'],
        description: [
          '例如设计会员体系或特色菜品时，每轮最多询问三个会明显影响方案方向的问题，不要求你先学会写复杂提示词。',
          'For tasks such as membership plans or signature dishes, each round asks at most three questions that materially affect the answer. You do not need to learn complex prompting.'
        ],
        location: [
          ['餐饮任务', 'Restaurant tasks'],
          ['直接说个大概', 'Start with a rough idea']
        ]
      },
      {
        icon: 'check',
        title: ['答案都可以直接点击', 'Tap to answer'],
        description: [
          '可点击预设答案，也可选择“其他 / 我来说明”或“你帮我决定”；选中状态和当前需求会立即显示。',
          'Choose a suggested answer, add your own note, or select “Decide for me.” Selections and the current brief update immediately.'
        ],
        location: [
          ['澄清卡片', 'Clarification card'],
          ['点击选项', 'Tap a choice']
        ]
      },
      {
        icon: 'plan',
        title: ['先看任务简报，再决定生成', 'Review the brief before generating'],
        description: [
          '信息足够后会整理成任务简报，你可以按当前需求直接生成，也可以继续补充，不会被迫完成冗长问卷。',
          'When enough is known, the Agent shows a task brief. Generate from it or add more context without being forced through a long questionnaire.'
        ],
        location: [
          ['任务简报', 'Task brief'],
          ['按当前需求生成', 'Generate']
        ]
      },
      {
        icon: 'info',
        title: ['餐厅档案无需专门维护', 'No profile setup to maintain'],
        description: [
          '只有任务中出现适合长期复用的稳定信息时，系统才会提议保存；必须由你确认，默认只用于当前任务。',
          'The system only proposes saving stable, reusable facts during a task. Nothing is stored without your confirmation, and the default is task-only.'
        ],
        location: [
          ['任务内提议', 'In-task proposal'],
          ['确认后才保存', 'Save only after confirmation']
        ]
      }
    ],
    note: [
      '餐饮引导仅在“餐饮任务”工作台生效，你可以随时切回“通用聊天”。卡片选项只补充当前需求，不会自动修改长期档案。',
      'Restaurant guidance applies only in the Restaurant workbench, and you can switch back to General chat at any time. Card selections refine the current task and never silently change the saved profile.'
    ]
  };

  const dispatch = createEventDispatcher<{ dismiss: void }>();
  let dialogElement: HTMLDivElement;

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: release = restaurantExperience ? restaurantRelease : generalRelease;

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
        <span><Icon name="sparkles" size={19} /></span>
        <div>
          <strong>{t('更新公告', 'What’s new')}</strong>
          <small>La4RainGPT · 2026.07.28</small>
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
        <span class="release-tag">{t(...release.tag)}</span>
        <h2 id="update-title">{t(...release.title)}</h2>
        <p id="update-description">{t(...release.description)}</p>
      </div>

      <div class="feature-list">
        {#each release.features as feature}
          <article>
            <span class="feature-icon"><Icon name={feature.icon} size={18} /></span>
            <div>
              <strong>{t(...feature.title)}</strong>
              <p>{t(...feature.description)}</p>
              <span class="location">
                {#each feature.location as location, index}
                  {#if index > 0}<i aria-hidden="true">›</i>{/if}
                  <span>{t(...location)}</span>
                {/each}
              </span>
            </div>
          </article>
        {/each}
      </div>

      <aside>
        <Icon name="info" size={17} />
        <span>{t(...release.note)}</span>
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
