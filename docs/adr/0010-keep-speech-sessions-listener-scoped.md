# Keep speech sessions listener-scoped and provider-neutral

La4RainGPT treats answer generation as durable background work but speech as an
ephemeral listener-owned projection of visible answer text. Speech therefore
uses a provider-neutral boundary, never receives reasoning or tool activity,
stops when its listener disconnects, and is not persisted or resumed
automatically; this avoids vendor lock-in, unwanted TTS charges, duplicate
playback across reconnects, and growth against conversation storage allowances.
