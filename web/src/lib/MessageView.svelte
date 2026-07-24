<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import type { Message, MessagePart } from './types';
  import { attachmentURL } from './api';
  import { translate, type Locale } from './i18n';
  import Markdown from './Markdown.svelte';

  export let message: Message;
  export let locale: Locale = 'zh-CN';
  export let canRegenerate = false;
  let copied = false;
  const dispatch = createEventDispatcher<{ regenerate: { message: Message } }>();

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);

  function toolLabel(part: MessagePart): string {
    const type = String(part.data?.type || '');
    if (type === 'web_search') return t('网页搜索', 'Web search');
    if (type === 'image_generation') return t('图像生成', 'Image generation');
    return t('工具调用', 'Tool call');
  }

  function toolDescription(part: MessagePart): string {
    const status = String(part.data?.status || '');
    const duration = Number(part.data?.durationMs || 0);
    const suffix =
      duration > 0
        ? ` · ${(duration / 1000).toFixed(duration >= 1000 ? 1 : 2)} ${t('秒', 's')}`
        : '';
    if (status === 'completed') return `${t('已完成', 'Completed')}${suffix}`;
    if (status === 'failed') {
      return `${t('失败', 'Failed')}${part.data?.errorCode ? ` · ${part.data.errorCode}` : ''}${suffix}`;
    }
    return t('运行中', 'Running');
  }

  function reasoningDuration(part: MessagePart): string {
    const duration = Number(part.data?.durationMs || 0);
    if (duration <= 0) return '';
    return `${(duration / 1000).toFixed(duration >= 1000 ? 1 : 2)} ${t('秒', 's')}`;
  }

  function toolQuery(part: MessagePart): string {
    const data = part.data?.data;
    if (!data || typeof data !== 'object') return '';
    const query = (data as Record<string, unknown>).query;
    return typeof query === 'string' ? query : '';
  }

  function toolData(part: MessagePart): string {
    const data = part.data?.data;
    if (!data || typeof data !== 'object' || Array.isArray(data)) return '';
    return JSON.stringify(data, null, 2);
  }

  function citations(part: MessagePart): Array<{ url: string; title?: string }> {
    const value = part.data?.citations;
    return Array.isArray(value) ? value : [];
  }

  function citationLabel(citation: { url: string; title?: string }): string {
    if (citation.title) return citation.title;
    try {
      return new URL(citation.url).hostname;
    } catch {
      return citation.url;
    }
  }

  async function copyAnswer() {
    const value = message.parts
      .filter((part) => part.type === 'text')
      .map((part) => part.text || '')
      .join('\n\n');
    if (!value) return;
    await navigator.clipboard.writeText(value);
    copied = true;
    setTimeout(() => (copied = false), 1400);
  }
</script>

<article class:user={message.role === 'user'} class:assistant={message.role === 'assistant'} class="message">
  <div class="avatar" aria-hidden="true">
    {#if message.role === 'user'}{t('你', 'You')}{:else}<span>✦</span>{/if}
  </div>
  <div class="message-body">
    {#each message.parts as part}
      {#if part.type === 'reasoning' && part.text}
        <details class="reasoning" open={message.status === 'streaming'}>
          <summary>
            <span class="reasoning-spark">✦</span>
            {t('推理摘要', 'Reasoning summary')}
            {#if reasoningDuration(part)}
              <span class="reasoning-duration">· {reasoningDuration(part)}</span>
            {/if}
            {#if message.status === 'streaming'}<span class="pulse-dot"></span>{/if}
          </summary>
          <div class="reasoning-content"><Markdown text={part.text} {locale} /></div>
        </details>
      {:else if part.type === 'text' && part.text}
        <div class="answer"><Markdown text={part.text} {locale} /></div>
      {:else if part.type === 'image' && part.attachmentId}
        <a
          class="message-image-link"
          href={attachmentURL(part.attachmentId)}
          target="_blank"
          rel="noopener noreferrer"
        >
          <img
            class="message-image"
            src={attachmentURL(part.attachmentId)}
            alt={message.role === 'assistant'
              ? t('生成的图片', 'Generated image')
              : t('上传的图片', 'Uploaded image')}
            loading="lazy"
          />
        </a>
      {:else if part.type === 'tool'}
        <div
          class="tool-card"
          class:completed={part.data?.status === 'completed'}
          class:failed={part.data?.status === 'failed'}
        >
          <div class="tool-icon">{part.data?.type === 'web_search' ? '⌕' : '◇'}</div>
          <div>
            <strong>{toolLabel(part)}</strong>
            <span>{toolDescription(part)}</span>
            {#if toolQuery(part)}<small>{toolQuery(part)}</small>{/if}
            {#if toolData(part)}
              <details class="tool-details">
                <summary>{t('查看安全参数', 'View sanitized parameters')}</summary>
                <pre>{toolData(part)}</pre>
              </details>
            {/if}
          </div>
          {#if part.data?.status === 'in_progress'}<span class="tool-spinner"></span>{/if}
        </div>
      {:else if part.type === 'citations'}
        <div class="citations">
          <div class="citation-title">{t('引用来源', 'Sources')}</div>
          <div class="citation-row">
            {#each citations(part) as citation, index}
              <a href={citation.url} target="_blank" rel="noopener noreferrer">
                <span>{index + 1}</span>{citationLabel(citation)}
              </a>
            {/each}
          </div>
        </div>
      {/if}
    {/each}

    {#if message.status === 'streaming' && message.parts.length === 0}
      <div class="typing"><span>{t('正在思考', 'Thinking')}</span><i></i><i></i><i></i></div>
    {/if}
    {#if message.status === 'error' || message.status === 'interrupted'}
      <div class="message-error">
        {message.status === 'interrupted'
          ? t('回答被中断', 'Response interrupted')
          : t('回答失败', 'Response failed')}
        {#if message.errorCode}<span>· {message.errorCode}</span>{/if}
      </div>
    {/if}
    {#if message.role === 'assistant' && message.status !== 'streaming'}
      <div class="message-footer">
        {#if message.parts.some((part) => part.type === 'text' && part.text)}
          <button class="copy-answer" title={t('复制回答', 'Copy response')} on:click={copyAnswer}>
            {copied ? `✓ ${t('已复制', 'Copied')}` : `▣ ${t('复制', 'Copy')}`}
          </button>
        {/if}
        {#if canRegenerate}
          <button
            class="copy-answer"
            title={message.status === 'completed'
              ? t('重新生成', 'Regenerate')
              : t('重试', 'Retry')}
            on:click={() => dispatch('regenerate', { message })}
          >
            ↻ {message.status === 'completed'
              ? t('重新生成', 'Regenerate')
              : t('重试', 'Retry')}
          </button>
        {/if}
        {#if message.status === 'completed'}
          <div class="message-meta">
            {message.model}
            {#if message.reasoningEffortRequested}
              <span>
                · {t('推理', 'Reasoning')} {message.reasoningEffortRequested === 'auto'
                  ? t('自动', 'Auto')
                  : message.reasoningEffortSent || message.reasoningEffortRequested}
              </span>
            {/if}
            {#if message.outputTokens}
              <span>· {message.outputTokens.toLocaleString(locale)} tokens</span>
            {/if}
          </div>
        {/if}
      </div>
    {/if}
  </div>
</article>
