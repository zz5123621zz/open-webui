<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import type { Message, MessagePart } from './types';
  import { attachmentURL } from './api';
  import { translate, type Locale } from './i18n';
  import Icon from './Icon.svelte';
  import Markdown from './Markdown.svelte';
  import SpeechMessageControl from './SpeechMessageControl.svelte';

  export let message: Message;
  export let locale: Locale = 'zh-CN';
  export let canRegenerate = false;
  export let streamingStage = '';
  export let streamNow = Date.now();
  export let elapsedSeconds = 0;
  let copied = false;
  const dispatch = createEventDispatcher<{ regenerate: { message: Message } }>();

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: reasoningCount = message.parts.filter(
    (part) => part.type === 'reasoning' && part.text
  ).length;

  function toolLabel(part: MessagePart): string {
    const type = String(part.data?.type || '');
    if (type === 'web_search') return t('网页搜索', 'Web search');
    if (type === 'image_generation') return t('图像生成', 'Image generation');
    return t('工具调用', 'Tool call');
  }

  function toolDescription(part: MessagePart): string {
    const status = String(part.data?.status || '');
    const type = String(part.data?.type || '');
    const startedAt = Number(part.data?.clientStartedAt || 0);
    const duration =
      status === 'in_progress' && startedAt > 0
        ? Math.max(0, streamNow - startedAt)
        : Number(part.data?.durationMs || 0);
    const suffix =
      duration > 0
        ? ` · ${(duration / 1000).toFixed(duration >= 1000 ? 1 : 2)} ${t('秒', 's')}`
        : '';
    if (status === 'completed') {
      const label =
        type === 'web_search'
          ? t('搜索完成', 'Search complete')
          : type === 'image_generation'
            ? t('图片生成完成', 'Image complete')
            : t('执行完成', 'Completed');
      return `${label}${suffix}`;
    }
    if (status === 'failed') {
      return `${t('失败', 'Failed')}${part.data?.errorCode ? ` · ${part.data.errorCode}` : ''}${suffix}`;
    }
    if (
      status === 'in_progress' &&
      (message.status === 'interrupted' || message.status === 'error')
    ) {
      return message.status === 'interrupted'
        ? t('随回答一同中断', 'Interrupted with the response')
        : t('随回答一同结束', 'Ended with the response');
    }
    const running =
      type === 'web_search'
        ? t('正在搜索网页', 'Searching the web')
        : type === 'image_generation'
          ? t('正在生成图片', 'Generating the image')
          : t('正在执行工具', 'Running tool');
    return duration > 0
      ? `${running} · ${t('已运行', 'Running for')} ${Math.floor(duration / 1000)} s`
      : running;
  }

  function reasoningDuration(part: MessagePart): string {
    const providerDuration = Number(part.data?.durationMs || 0);
    if (providerDuration > 0) {
      return `${(providerDuration / 1000).toFixed(providerDuration >= 1000 ? 1 : 2)} ${t('秒', 's')}`;
    }
    if (message.status === 'streaming' && part.data?.completed !== true) {
      const completedDuration = Number(part.data?.clientDurationMs || 0);
      if (completedDuration > 0) {
        return `${(completedDuration / 1000).toFixed(completedDuration >= 1000 ? 1 : 2)} ${t('秒', 's')}`;
      }
      const startedAt = Number(part.data?.clientStartedAt || 0);
      const seconds =
        startedAt > 0
          ? Math.max(0, Math.floor((streamNow - startedAt) / 1000))
          : elapsedSeconds;
      return `${t('已运行', 'Running for')} ${seconds} s`;
    }
    const duration = Number(part.data?.clientDurationMs || 0);
    if (duration <= 0) return '';
    return `${(duration / 1000).toFixed(duration >= 1000 ? 1 : 2)} ${t('秒', 's')}`;
  }

  function reasoningOrdinal(part: MessagePart): number {
    return message.parts
      .filter((candidate) => candidate.type === 'reasoning' && candidate.text)
      .indexOf(part) + 1;
  }

  function responseErrorDescription(code: string | undefined): string {
    const descriptions: Record<string, [string, string]> = {
      response_cancelled: ['已由你主动停止', 'Stopped by you'],
      service_interrupted: ['服务重启时中断，已保留现有内容', 'Interrupted by a service restart; saved content was retained'],
      response_timeout: ['运行超过 30 分钟，已自动停止', 'Stopped after exceeding 30 minutes'],
      response_interrupted: ['生成过程意外中断', 'Generation was interrupted unexpectedly'],
      provider_stream_incomplete: ['模型未发送完整的结束事件', 'The model did not send a complete ending event'],
      persistence_failed: ['回答进度无法安全保存', 'Response progress could not be saved safely']
    };
    if (!code) return '';
    const description = descriptions[code];
    return description ? t(description[0], description[1]) : code;
  }

  type ToolDetail = { label: string; value: string; url?: string };

  function toolDetails(part: MessagePart): ToolDetail[] {
    const data = part.data?.data;
    if (!data || typeof data !== 'object' || Array.isArray(data)) return [];
    const action = data as Record<string, unknown>;
    const details: ToolDetail[] = [];
    if (typeof action.query === 'string' && action.query.trim()) {
      details.push({
        label: t('搜索内容', 'Search query'),
        value: action.query.trim()
      });
    }
    if (typeof action.url === 'string') {
      const url = safeHTTPURL(action.url);
      if (url) {
        details.push({
          label: t('访问网页', 'Visited page'),
          value: citationLabel({ url }),
          url
        });
      }
    }
    if (typeof action.pattern === 'string' && action.pattern.trim()) {
      details.push({
        label: t('页内查找', 'Find on page'),
        value: action.pattern.trim()
      });
    }
    if (action.explicit === true) {
      details.push({
        label: t('任务方式', 'Task mode'),
        value: t(
          '由你主动发起 · 图片质量自动选择',
          'Started by you · image quality selected automatically'
        )
      });
    }
    return details;
  }

  function safeHTTPURL(value: string): string {
    try {
      const parsed = new URL(value);
      return parsed.protocol === 'https:' || parsed.protocol === 'http:' ? parsed.href : '';
    } catch {
      return '';
    }
  }

  function toolSources(part: MessagePart): Array<{ url: string; title?: string }> {
    if (String(part.data?.type || '') !== 'web_search') return [];
    const webParts = message.parts.filter(
      (candidate) => candidate.type === 'tool' && candidate.data?.type === 'web_search'
    );
    if (webParts.at(-1) !== part) return [];
    const seen = new Set<string>();
    const result: Array<{ url: string; title?: string }> = [];
    for (const candidate of message.parts) {
      if (candidate.type !== 'citations') continue;
      for (const citation of citations(candidate)) {
        const url = safeHTTPURL(citation.url);
        if (!url || seen.has(url)) continue;
        seen.add(url);
        result.push({ ...citation, url });
        if (result.length === 5) return result;
      }
    }
    return result;
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
    {#if message.role === 'user'}{t('你', 'You')}{:else}<Icon name="sparkles" size={15} />{/if}
  </div>
  <div class="message-body">
    {#each message.parts as part}
      {#if part.type === 'reasoning' && part.text}
        <details class="reasoning" open={message.status === 'streaming'}>
          <summary>
            <span class="reasoning-spark"><Icon name="sparkles" size={13} /></span>
            {t('推理摘要', 'Reasoning summary')}
            {#if reasoningCount > 1}
              <span class="reasoning-index">
                {t(`第 ${reasoningOrdinal(part)} 段`, `Section ${reasoningOrdinal(part)}`)}
              </span>
            {/if}
            {#if reasoningDuration(part)}
              <span class="reasoning-duration">· {reasoningDuration(part)}</span>
            {/if}
            {#if message.status === 'streaming' && part.data?.completed !== true}
              <span class="pulse-dot"></span>
            {/if}
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
        {@const details = toolDetails(part)}
        {@const sources = toolSources(part)}
        <div
          class="tool-card"
          class:completed={part.data?.status === 'completed'}
          class:failed={part.data?.status === 'failed'}
        >
          <div class="tool-icon" aria-hidden="true">
            <Icon
              name={part.data?.type === 'web_search' ? 'search' : 'image'}
              size={17}
            />
          </div>
          <div>
            <strong>{toolLabel(part)}</strong>
            <span>{toolDescription(part)}</span>
            {#if details.length}
              <div class="tool-targets">
                {#each details as detail}
                  <small>
                    <b>{detail.label}</b>
                    {#if detail.url}
                      <a href={detail.url} target="_blank" rel="noopener noreferrer">
                        {detail.value}
                      </a>
                    {:else}
                      <span>{detail.value}</span>
                    {/if}
                  </small>
                {/each}
              </div>
            {/if}
            {#if sources.length}
              <details class="tool-details">
                <summary>
                  {t('查看搜索来源', 'View search sources')} · {sources.length}
                </summary>
                <div class="tool-source-list">
                  {#each sources as source}
                    <a href={source.url} target="_blank" rel="noopener noreferrer">
                      <Icon name="globe" size={13} />
                      {citationLabel(source)}
                    </a>
                  {/each}
                </div>
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

    {#if message.status === 'streaming'}
      <div class:compact={message.parts.length > 0} class="thinking-progress" role="status">
        <span class="thinking-progress-icon"><Icon name="sparkles" size={15} /></span>
        <span class="thinking-progress-copy">
          <strong>{streamingStage || t('正在思考', 'Thinking')}</strong>
          <small>
            {t('已运行', 'Running for')} {elapsedSeconds} s
            · {t(
              '等待新事件；这里只显示真实阶段与 Provider 摘要',
              'Waiting for a new event; only factual stages and provider summaries are shown'
            )}
          </small>
        </span>
        <span class="tool-spinner" aria-hidden="true"></span>
      </div>
    {/if}
    {#if message.status === 'error' || message.status === 'interrupted'}
      <div class="message-error" role="alert">
        <Icon name="alert" size={15} />
        {message.status === 'interrupted'
          ? message.errorCode === 'response_cancelled'
            ? t('回答已停止', 'Response stopped')
            : t('回答被中断', 'Response interrupted')
          : t('回答失败', 'Response failed')}
        {#if responseErrorDescription(message.errorCode)}
          <span>· {responseErrorDescription(message.errorCode)}</span>
        {/if}
      </div>
    {/if}
    {#if message.role === 'assistant' &&
      message.parts.some((part) => part.type === 'text' && part.text)}
      <div class:streaming={message.status === 'streaming'} class="message-footer">
        <SpeechMessageControl {message} {locale} />
        {#if message.status !== 'streaming'}
          <button
            class="copy-answer"
            aria-label={t('复制回答', 'Copy response')}
            title={t('复制回答', 'Copy response')}
            on:click={copyAnswer}
          >
            <Icon name={copied ? 'check' : 'copy'} size={14} />
            {copied ? t('已复制', 'Copied') : t('复制', 'Copy')}
          </button>
        {/if}
        {#if canRegenerate && message.status !== 'streaming'}
          <button
            class="copy-answer"
            title={message.status === 'completed'
              ? t('重新生成', 'Regenerate')
              : t('重试', 'Retry')}
            aria-label={message.status === 'completed'
              ? t('重新生成', 'Regenerate')
              : t('重试', 'Retry')}
            on:click={() => dispatch('regenerate', { message })}
          >
            <Icon name="retry" size={14} />
            {message.status === 'completed'
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
