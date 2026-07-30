<script lang="ts">
  import { createEventDispatcher, onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import { translate, type Locale } from './i18n';
  import type { DictationAvailability, DictationPhase } from './types';

  export let locale: Locale = 'zh-CN';
  export let availability: DictationAvailability | null = null;
  export let draft = '';
  export let disabled = false;
  export let sessionKey = '';
  export let active = false;
  export let phase: DictationPhase = 'idle';
  export let elapsedSeconds = 0;

  const dispatch = createEventDispatcher<{
    start: void;
    text: { value: string; transcript: string; final: boolean };
    cancelled: void;
    completed: void;
    error: { code: string; message: string };
  }>();

  type AudioContextFactory = new (options?: AudioContextOptions) => AudioContext;
  type WebkitWindow = Window & { webkitAudioContext?: AudioContextFactory };

  let mounted = false;
  let observedSessionKey = '';
  let supported = false;
  let supportError = '';
  let socket: WebSocket | null = null;
  let stream: MediaStream | null = null;
  let audioContext: AudioContext | null = null;
  let sourceNode: MediaStreamAudioSourceNode | null = null;
  let processingNode: AudioNode | null = null;
  let workletNode: AudioWorkletNode | null = null;
  let scriptNode: ScriptProcessorNode | null = null;
  let silentGain: GainNode | null = null;
  let scriptSamples: number[] = [];
  let flushResolve: (() => void) | null = null;
  let originalDraft = '';
  let latestTranscript = '';
  let runID = 0;
  let terminal = true;
  let recordingStartedAt = 0;
  let elapsedTimer: number | undefined;
  let durationTimer: number | undefined;
  let connectionTimer: number | undefined;
  let holdTimer: number | undefined;
  let pointerSession = false;
  let holdGesture = false;
  let suppressClick = false;
  let finishingPromise: Promise<void> | null = null;

  $: t = (chinese: string, english: string) => translate(locale, chinese, english);
  $: unavailableReason = availabilityReason();
  $: buttonDisabled = !active && (disabled || Boolean(unavailableReason));
  $: buttonTitle = active
    ? phase === 'finishing'
      ? t('正在完成识别', 'Finishing transcription')
      : t('停止语音输入', 'Stop voice input')
    : unavailableReason ||
      t(
        '点击开始/停止；也可以按住说话，松开结束',
        'Click to start/stop, or hold to talk and release to finish'
      );

  $: if (mounted && sessionKey !== observedSessionKey) {
    observedSessionKey = sessionKey;
    if (active) void cancel();
  }

  onMount(() => {
    mounted = true;
    observedSessionKey = sessionKey;
    detectSupport();
    const stopWhenHidden = () => {
      if (document.visibilityState === 'hidden' && active) {
        void stop(true);
      }
    };
    const stopOnPageHide = () => {
      if (active) void stop(true);
    };
    document.addEventListener('visibilitychange', stopWhenHidden);
    window.addEventListener('pagehide', stopOnPageHide);
    return () => {
      mounted = false;
      document.removeEventListener('visibilitychange', stopWhenHidden);
      window.removeEventListener('pagehide', stopOnPageHide);
      void terminate(false, false);
    };
  });

  function detectSupport() {
    const AudioContextConstructor =
      window.AudioContext || (window as WebkitWindow).webkitAudioContext;
    const mediaDevices = Reflect.get(navigator, 'mediaDevices') as
      | MediaDevices
      | undefined;
    const getUserMedia = mediaDevices
      ? Reflect.get(mediaDevices, 'getUserMedia')
      : undefined;
    const WebSocketConstructor = Reflect.get(window, 'WebSocket');
    supported = Boolean(
      window.isSecureContext &&
        typeof getUserMedia === 'function' &&
        typeof WebSocketConstructor === 'function' &&
        AudioContextConstructor
    );
    if (supported) {
      supportError = '';
      return;
    }
    supportError = /MicroMessenger/i.test(navigator.userAgent)
      ? t(
          '当前微信浏览器不支持可靠录音，请用 Safari 打开',
          'This WeChat browser cannot record reliably; open the site in Safari'
        )
      : t(
          '当前浏览器不支持语音输入，请改用 Safari 或桌面 Edge',
          'Voice input is unsupported here; use Safari or desktop Edge'
        );
  }

  function availabilityReason(): string {
    if (!supported) return supportError;
    if (!availability) {
      return t('正在读取语音输入状态', 'Loading voice input status');
    }
    if (!availability.enabled) {
      return t('管理员已关闭语音输入', 'Voice input is disabled by the administrator');
    }
    if (!availability.configured) {
      return t('语音识别服务尚未配置', 'Speech recognition is not configured');
    }
    return '';
  }

  function websocketURL(): string {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${protocol}//${window.location.host}/api/v1/dictation/sessions`;
  }

  async function begin() {
    if (active || buttonDisabled) return;
    const currentRun = ++runID;
    terminal = false;
    originalDraft = draft;
    latestTranscript = '';
    active = true;
    phase = 'requesting';
    elapsedSeconds = 0;
    dispatch('start');
    try {
      const AudioContextConstructor =
        window.AudioContext || (window as WebkitWindow).webkitAudioContext;
      if (!AudioContextConstructor) {
        throw new Error('audio_context_unavailable');
      }
      audioContext = new AudioContextConstructor({ latencyHint: 'interactive' });
      await audioContext.resume();
      stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true
        },
        video: false
      });
      if (currentRun !== runID || terminal) return;
      await prepareRecorder();
      if (currentRun !== runID || terminal) return;
      phase = 'connecting';
      socket = new WebSocket(websocketURL());
      socket.binaryType = 'arraybuffer';
      socket.onmessage = (event) => {
        void handleSocketMessage(currentRun, event);
      };
      socket.onerror = () => {
        // Browsers intentionally hide WebSocket handshake details. onclose
        // supplies the single recoverable error if no structured event arrived.
      };
      socket.onclose = () => {
        if (currentRun === runID && !terminal) {
          void fail(
            'dictation_connection_closed',
            t(
              '语音识别连接意外中断，已保留最后识别出的文字。',
              'The transcription connection closed; the latest text was kept.'
            )
          );
        }
      };
      connectionTimer = window.setTimeout(() => {
        if (currentRun === runID && !terminal && phase !== 'listening') {
          void fail(
            'dictation_connection_timeout',
            t(
              '连接语音识别服务超时，请稍后重试。',
              'Connecting to speech recognition timed out. Try again.'
            )
          );
        }
      }, 15000);
    } catch (value) {
      if (currentRun !== runID || terminal) return;
      const error = value as DOMException;
      const permissionDenied =
        error?.name === 'NotAllowedError' || error?.name === 'SecurityError';
      const noDevice = error?.name === 'NotFoundError';
      let message = t(
        '无法启动麦克风，请检查浏览器权限后重试。',
        'The microphone could not start. Check browser permissions and try again.'
      );
      if (permissionDenied) {
        message = /MicroMessenger/i.test(navigator.userAgent)
          ? t(
              '微信浏览器未允许麦克风，请检查权限；仍不可用时请用 Safari 打开。',
              'WeChat did not allow microphone access. Check its permission or open the site in Safari.'
            )
          : t(
              '麦克风权限被拒绝，请在浏览器设置中允许本站使用麦克风。',
              'Microphone permission was denied. Allow it for this site in browser settings.'
            );
      } else if (noDevice) {
        message = t(
          '没有找到可用的麦克风。',
          'No available microphone was found.'
        );
      }
      await fail('dictation_microphone_failed', message);
    }
  }

  async function prepareRecorder() {
    if (!audioContext || !stream) {
      throw new Error('dictation_recorder_not_ready');
    }
    sourceNode = audioContext.createMediaStreamSource(stream);
    silentGain = audioContext.createGain();
    silentGain.gain.value = 0;
    silentGain.connect(audioContext.destination);

    if (audioContext.audioWorklet && typeof AudioWorkletNode !== 'undefined') {
      try {
        await audioContext.audioWorklet.addModule('/dictation-processor.js');
        workletNode = new AudioWorkletNode(
          audioContext,
          'la4rain-dictation-processor',
          {
            numberOfInputs: 1,
            numberOfOutputs: 1,
            outputChannelCount: [1]
          }
        );
        workletNode.port.onmessage = (event) => {
          if (event.data?.type === 'pcm' && event.data.buffer instanceof ArrayBuffer) {
            sendPCM(event.data.buffer);
          } else if (event.data?.type === 'flushed') {
            flushResolve?.();
            flushResolve = null;
          }
        };
        processingNode = workletNode;
        return;
      } catch {
        workletNode = null;
      }
    }

    scriptNode = audioContext.createScriptProcessor(4096, 1, 1);
    scriptNode.onaudioprocess = (event) => {
      const input = event.inputBuffer.getChannelData(0);
      for (let index = 0; index < input.length; index += 1) {
        scriptSamples.push(input[index]);
      }
      const target = Math.max(1, Math.round((audioContext?.sampleRate || 48000) * 0.2));
      while (scriptSamples.length >= target) {
        const samples = Float32Array.from(scriptSamples.splice(0, target));
        sendPCM(resamplePCM(samples, audioContext?.sampleRate || 48000));
      }
    };
    processingNode = scriptNode;
  }

  function startRecorder() {
    if (!sourceNode || !processingNode || !silentGain) {
      throw new Error('dictation_recorder_not_ready');
    }
    sourceNode.connect(processingNode);
    processingNode.connect(silentGain);
  }

  async function handleSocketMessage(
    currentRun: number,
    event: MessageEvent
  ) {
    if (currentRun !== runID || terminal || typeof event.data !== 'string') return;
    let message: Record<string, unknown>;
    try {
      message = JSON.parse(event.data) as Record<string, unknown>;
    } catch {
      await fail(
        'dictation_protocol_failed',
        t(
          '语音识别服务返回了无法解析的数据。',
          'Speech recognition returned unreadable data.'
        )
      );
      return;
    }
    switch (message.type) {
      case 'dictation.ready':
        socket?.send(
          JSON.stringify({ type: 'dictation.start', draft: originalDraft })
        );
        break;
      case 'dictation.connecting':
        phase = 'connecting';
        break;
      case 'dictation.started': {
        window.clearTimeout(connectionTimer);
        const serverMaximum = Number(message.maxDurationSeconds);
        const maximum =
          Number.isFinite(serverMaximum) && serverMaximum > 0
            ? serverMaximum
            : availability?.maxDurationSeconds || 120;
        try {
          await audioContext?.resume();
          startRecorder();
        } catch {
          await fail(
            'dictation_recorder_failed',
            t(
              '浏览器无法处理麦克风音频，请刷新后重试。',
              'The browser could not process microphone audio. Reload and try again.'
            )
          );
          return;
        }
        phase = 'listening';
        recordingStartedAt = Date.now();
        updateElapsed();
        elapsedTimer = window.setInterval(updateElapsed, 250);
        durationTimer = window.setTimeout(() => {
          if (currentRun === runID && phase === 'listening') void stop();
        }, maximum * 1000);
        break;
      }
      case 'dictation.transcript':
        applyTranscript(String(message.text || ''), false);
        break;
      case 'dictation.stopping':
        if (phase === 'listening') {
          phase = 'finishing';
          stopElapsedTimers();
          await haltRecorder(false);
        }
        break;
      case 'dictation.completed':
        applyTranscript(String(message.text || latestTranscript), true);
        await terminate(true, false);
        dispatch('completed');
        break;
      case 'dictation.cancelled':
        await terminate(false, true);
        break;
      case 'dictation.error':
        await fail(
          String(message.code || 'dictation_failed'),
          localizedServerError(
            String(message.code || ''),
            String(message.message || '')
          )
        );
        break;
    }
  }

  function updateElapsed() {
    elapsedSeconds = recordingStartedAt
      ? Math.max(0, Math.floor((Date.now() - recordingStartedAt) / 1000))
      : 0;
  }

  function applyTranscript(transcript: string, final: boolean) {
    transcript = transcript.trim();
    if (transcript) latestTranscript = transcript;
    const effective = transcript || latestTranscript;
    dispatch('text', {
      value: composeDraft(effective),
      transcript: effective,
      final
    });
  }

  function composeDraft(transcript: string): string {
    if (!transcript) return originalDraft;
    if (!originalDraft) return transcript;
    const separator = /\s$/u.test(originalDraft) ? '' : '\n';
    return `${originalDraft}${separator}${transcript}`;
  }

  function sendPCM(buffer: ArrayBuffer) {
    if (
      !active ||
      terminal ||
      !socket ||
      socket.readyState !== WebSocket.OPEN ||
      !['listening', 'finishing'].includes(phase)
    ) return;
    if (socket.bufferedAmount > 512 * 1024) {
      void fail(
        'dictation_network_slow',
        t(
          '网络无法及时上传麦克风音频，已停止并保留最后识别出的文字。',
          'The network could not keep up with microphone audio. Recording stopped and the latest text was kept.'
        )
      );
      return;
    }
    socket.send(buffer);
  }

  function resamplePCM(samples: Float32Array, sourceRate: number): ArrayBuffer {
    const targetLength = Math.max(1, Math.round(samples.length * 16000 / sourceRate));
    const output = new Int16Array(targetLength);
    for (let index = 0; index < targetLength; index += 1) {
      const start = Math.floor(index * samples.length / targetLength);
      const end = Math.max(
        start + 1,
        Math.floor((index + 1) * samples.length / targetLength)
      );
      let sum = 0;
      for (let cursor = start; cursor < end && cursor < samples.length; cursor += 1) {
        sum += samples[cursor];
      }
      const sample = Math.max(-1, Math.min(1, sum / Math.max(1, end - start)));
      output[index] = sample < 0 ? sample * 0x8000 : sample * 0x7fff;
    }
    return output.buffer;
  }

  export async function stop(quick = false) {
    if (!active || terminal) return;
    if (phase === 'requesting' || phase === 'connecting') {
      await cancel();
      return;
    }
    if (phase !== 'listening') return finishingPromise || undefined;
    if (finishingPromise) return finishingPromise;
    phase = 'finishing';
    stopElapsedTimers();
    finishingPromise = (async () => {
      await haltRecorder(!quick);
      if (socket?.readyState === WebSocket.OPEN && !terminal) {
        socket.send(JSON.stringify({ type: 'dictation.finish' }));
      } else if (!terminal) {
        await fail(
          'dictation_connection_closed',
          t(
            '语音识别连接已断开，已保留最后识别出的文字。',
            'The transcription connection closed; the latest text was kept.'
          )
        );
      }
    })();
    try {
      await finishingPromise;
    } finally {
      finishingPromise = null;
    }
  }

  export async function cancel() {
    if (!active || terminal) return;
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'dictation.cancel' }));
    }
    dispatch('text', {
      value: originalDraft,
      transcript: '',
      final: false
    });
    await terminate(false, true);
  }

  async function fail(code: string, message: string) {
    if (terminal) return;
    if (latestTranscript) {
      dispatch('text', {
        value: composeDraft(latestTranscript),
        transcript: latestTranscript,
        final: false
      });
    }
    await terminate(false, false);
    dispatch('error', { code, message });
  }

  async function terminate(completed: boolean, cancelled: boolean) {
    if (terminal && !active) return;
    terminal = true;
    runID += 1;
    stopElapsedTimers();
    window.clearTimeout(connectionTimer);
    window.clearTimeout(holdTimer);
    await haltRecorder(false);
    if (socket) {
      const current = socket;
      socket = null;
      current.onmessage = null;
      current.onerror = null;
      current.onclose = null;
      if (current.readyState === WebSocket.OPEN) {
        current.close(1000, completed ? 'completed' : cancelled ? 'cancelled' : 'stopped');
      } else if (current.readyState === WebSocket.CONNECTING) {
        try {
          current.close();
        } catch {
          // Safari can reject close() while the handshake is still pending.
        }
      }
    }
    phase = 'idle';
    active = false;
    pointerSession = false;
    holdGesture = false;
    if (cancelled) dispatch('cancelled');
  }

  async function haltRecorder(flush: boolean) {
    try {
      sourceNode?.disconnect();
    } catch {
      // The source may not have been connected yet.
    }
    if (flush) {
      if (workletNode) {
        await new Promise<void>((resolve) => {
          let resolved = false;
          const finish = () => {
            if (resolved) return;
            resolved = true;
            resolve();
          };
          flushResolve = finish;
          workletNode?.port.postMessage({ type: 'flush' });
          window.setTimeout(finish, 300);
        });
        flushResolve = null;
      } else if (scriptSamples.length && audioContext) {
        const remaining = Float32Array.from(scriptSamples);
        scriptSamples = [];
        sendPCM(resamplePCM(remaining, audioContext.sampleRate));
      }
    }
    if (scriptNode) scriptNode.onaudioprocess = null;
    try {
      processingNode?.disconnect();
      silentGain?.disconnect();
    } catch {
      // Nodes can already be disconnected during page teardown.
    }
    stream?.getTracks().forEach((track) => track.stop());
    stream = null;
    sourceNode = null;
    processingNode = null;
    workletNode = null;
    scriptNode = null;
    silentGain = null;
    scriptSamples = [];
    if (audioContext && audioContext.state !== 'closed') {
      try {
        await audioContext.close();
      } catch {
        // Safari may already have closed the context during page teardown.
      }
    }
    audioContext = null;
  }

  function stopElapsedTimers() {
    window.clearInterval(elapsedTimer);
    window.clearTimeout(durationTimer);
    elapsedTimer = undefined;
    durationTimer = undefined;
    updateElapsed();
  }

  function localizedServerError(code: string, fallback: string): string {
    const messages: Record<string, [string, string]> = {
      dictation_disabled: ['管理员已关闭语音输入。', 'Voice input is disabled.'],
      dictation_provider_unavailable: [
        '语音识别服务暂不可用，请稍后再试。',
        'Speech recognition is temporarily unavailable.'
      ],
      dictation_provider_not_granted: [
        '豆包语音识别资源尚未授权，请联系管理员检查实例。',
        'The Doubao speech recognition resource is not granted.'
      ],
      dictation_provider_auth_failed: [
        '豆包语音识别凭据无效，请联系管理员。',
        'The Doubao speech recognition credential is invalid.'
      ],
      dictation_provider_busy: [
        '豆包语音识别暂时繁忙，请稍后再试。',
        'Doubao speech recognition is temporarily busy.'
      ],
      dictation_session_limit: [
        '你已有一段录音正在识别，或当前两路识别均被占用。',
        'You already have an active recording, or both recognition slots are busy.'
      ],
      dictation_audio_empty: [
        '没有收到可识别的声音，请重新录音。',
        'No microphone audio was received. Record again.'
      ],
      dictation_no_speech: [
        '没有识别到清晰语音，请靠近麦克风后重试。',
        'No clear speech was recognized. Move closer to the microphone and try again.'
      ],
      dictation_audio_limit: [
        '单次语音输入最多两分钟，已停止录音。',
        'Voice input is limited to two minutes.'
      ],
      dictation_session_expired: [
        '本次语音输入已超时，请重新录音。',
        'This voice input session expired. Record again.'
      ]
    };
    const match = messages[code];
    return match
      ? t(match[0], match[1])
      : fallback ||
          t(
            '语音识别失败，已保留最后识别出的文字。',
            'Transcription failed; the latest recognized text was kept.'
          );
  }

  function handlePointerDown(event: PointerEvent) {
    if (buttonDisabled || phase === 'finishing') return;
    event.preventDefault();
    suppressClick = true;
    if (active) {
      void stop();
      return;
    }
    pointerSession = true;
    holdGesture = false;
    (event.currentTarget as HTMLButtonElement).setPointerCapture?.(event.pointerId);
    void begin();
    holdTimer = window.setTimeout(() => {
      if (pointerSession && active) holdGesture = true;
    }, 500);
  }

  function handlePointerUp(event: PointerEvent) {
    if (!pointerSession) return;
    event.preventDefault();
    pointerSession = false;
    window.clearTimeout(holdTimer);
    if (holdGesture) void stop();
    holdGesture = false;
  }

  function handlePointerCancel() {
    window.clearTimeout(holdTimer);
    if (pointerSession && active) void cancel();
    pointerSession = false;
    holdGesture = false;
    suppressClick = false;
  }

  function handleClick(event: MouseEvent) {
    if (suppressClick && event.detail !== 0) {
      suppressClick = false;
      event.preventDefault();
      return;
    }
    if (event.detail === 0) {
      if (active) void stop();
      else void begin();
    }
  }
</script>

<button
  type="button"
  class="toolbar-button dictation-button"
  class:active
  class:listening={phase === 'listening'}
  aria-label={buttonTitle}
  aria-pressed={active}
  title={buttonTitle}
  disabled={buttonDisabled}
  on:pointerdown={handlePointerDown}
  on:pointerup={handlePointerUp}
  on:pointercancel={handlePointerCancel}
  on:click={handleClick}
  on:contextmenu|preventDefault
>
  <Icon name={phase === 'finishing' ? 'stop' : 'microphone'} size={16} />
  <span>
    {active
      ? phase === 'finishing'
        ? t('识别中', 'Finishing')
        : t('停止', 'Stop')
      : t('语音输入', 'Voice input')}
  </span>
</button>
