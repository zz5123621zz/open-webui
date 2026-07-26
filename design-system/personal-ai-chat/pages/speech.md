# Speech & Read-Aloud Override

This feature specification extends `chat.md`. The chat page remains the visual
source of truth; speech controls must feel native to the existing workspace.

## Product intent

- Make long answers easier to follow without turning chat into a media app.
- Manual read-aloud is always obvious. Automatic read-aloud stays opt-in and
  requires an explicit audio-unlock gesture on each browser/device.
- Start playback from complete streamed sentences. Never read reasoning,
  tool-progress cards, citations, raw URLs, Markdown syntax or hidden metadata.
- Keep one active player per browser and one provider session per user.

## Surfaces

- Each assistant answer with text has a `朗读` action beside copy/regenerate.
  The action changes in place to pause/resume for the active answer.
- The global player sits inside the composer zone, directly above the composer.
  It contains status, play/pause, stop, ±10 seconds, progress, speed and volume.
- On mobile the player becomes a compact two-row surface. Primary controls stay
  44 × 44 px; secondary ranges use the full available width.
- User speech settings live in the profile menu. Administrator provider
  settings remain a separate admin-only entry.

## Visual language

- Solid `--surface` background, `--border`, 14 px radius and `--shadow-md`.
- Primary playback control uses `--primary`; secondary controls use muted text
  and a hover/pressed surface. Status is never represented by color alone.
- Use the existing outline icon family at 16 / 18 / 20 px. No waveform
  decoration, gradients, glass, album art or animated equalizer.
- Player labels are concise Chinese by default and keep full English parity.

## Interaction

- Button press feedback: `scale(0.97)` over 140–160 ms.
- Player entrance is an occasional state transition: opacity plus an 8 px
  translate over 200 ms with `cubic-bezier(0.23, 1, 0.32, 1)`.
- No animation on keyboard-triggered play/pause and no animation while seeking.
- Progress can seek only within received audio. During streaming, the buffered
  endpoint is exposed through `aria-valuemax` and a textual status.
- Changing speed updates the current buffered playback and persists the target
  provider speed for the next session.
- Stopping immediately cancels the WebSocket/provider session and releases all
  scheduled audio nodes.

## Accessibility and resilience

- Native buttons, selects and range inputs with visible labels or `aria-label`.
- Dynamic player state uses `role=status`; errors use `role=alert`.
- Space/Enter operate focused controls. Media Session actions are registered
  when supported without replacing browser-native shortcuts.
- `prefers-reduced-motion` removes the player translation while retaining a
  short opacity transition.
- Clear Chinese recovery messages cover authorization, provider availability,
  concurrency, network and unsupported audio formats.
- Audio remains in browser memory only and is cleared on stop, logout,
  conversation navigation or replacement by another answer.

## Release announcement

- The TTS launch announcement appears once per user, browser/device and release
  version. It uses browser-local storage and adds no backend state.
- If onboarding is still pending, it is shown first; the announcement opens
  only after onboarding is dismissed so dialogs never overlap.
- The announcement remains available from the profile menu and identifies the
  three user entry points: automatic reading, voice/speed settings, and the
  manual read-aloud action below each Agent answer.
- Desktop uses a centered dialog; mobile uses a safe-area-aware bottom sheet.
  Escape, backdrop, close, and confirmation all dismiss it, with a trapped
  keyboard focus order and 44 px controls.
