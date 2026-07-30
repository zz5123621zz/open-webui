class La4RainDictationProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.input = new Float32Array(Math.max(1, Math.round(sampleRate * 0.2)));
    this.length = 0;
    this.port.onmessage = (event) => {
      if (event.data?.type === 'flush') {
        this.flush();
        this.port.postMessage({ type: 'flushed' });
      }
    };
  }

  process(inputs) {
    const channel = inputs[0]?.[0];
    if (!channel?.length) return true;
    let offset = 0;
    while (offset < channel.length) {
      const count = Math.min(channel.length - offset, this.input.length - this.length);
      this.input.set(channel.subarray(offset, offset + count), this.length);
      this.length += count;
      offset += count;
      if (this.length === this.input.length) this.flush();
    }
    return true;
  }

  flush() {
    if (!this.length) return;
    const source = this.input.subarray(0, this.length);
    const targetLength = Math.max(1, Math.round(source.length * 16000 / sampleRate));
    const output = new Int16Array(targetLength);
    for (let index = 0; index < targetLength; index += 1) {
      const start = Math.floor(index * source.length / targetLength);
      const end = Math.max(start + 1, Math.floor((index + 1) * source.length / targetLength));
      let sum = 0;
      for (let cursor = start; cursor < end && cursor < source.length; cursor += 1) {
        sum += source[cursor];
      }
      const sample = Math.max(-1, Math.min(1, sum / Math.max(1, end - start)));
      output[index] = sample < 0 ? sample * 0x8000 : sample * 0x7fff;
    }
    this.port.postMessage({ type: 'pcm', buffer: output.buffer }, [output.buffer]);
    this.length = 0;
  }
}

registerProcessor('la4rain-dictation-processor', La4RainDictationProcessor);
