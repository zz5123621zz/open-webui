# Chat Workspace Override

This page-level specification overrides `../MASTER.md` for the signed-in chat
workspace and login screen.

## Product intent

- A private three-account AI chat tool, not a marketing page or dashboard.
- Content-first, quiet and trustworthy. The interface should recede while long
  answers, citations, images and tool progress remain easy to scan.
- Mobile-first at 375 px, then adapt at 768 / 1024 / 1440 px.

## Visual language

- Style: calm utility minimalism with solid surfaces and restrained elevation.
- Light mode is the default. Avoid AI-purple/pink gradients, glass decoration,
  oversized display typography, skeuomorphism and dense dashboard chrome.
- Use the bundled `Noto Sans SC Variable` for headings, UI and body text.
- Use one outline SVG icon family with 1.8 px strokes and 16 / 18 / 20 px sizes.
- Use a 4 / 8 px spacing rhythm and 12 / 16 / 20 px radius scale.

## Semantic color tokens

| Role | Light | Dark |
|---|---:|---:|
| Canvas | `#F5F7FA` | `#0F141B` |
| Sidebar | `#F8F9FB` | `#131922` |
| Surface | `#FFFFFF` | `#171E28` |
| Surface soft | `#F0F3F7` | `#202936` |
| Primary text | `#18212F` | `#EDF2F7` |
| Secondary text | `#556174` | `#B5C0CF` |
| Muted text | `#748094` | `#8D9AAF` |
| Border | `#E0E5EC` | `#2A3543` |
| Primary | `#2563EB` | `#7DA2FF` |
| Primary soft | `#EAF1FF` | `#1D3158` |
| Success | `#0F806D` | `#58C7B1` |
| Danger | `#C43D4B` | `#FF8E99` |
| Focus ring | `#2563EB` | `#8EADFF` |

All normal text pairs must meet WCAG AA. Statuses combine icon, label and color.

## Layout

- Desktop: 288 px persistent sidebar, 64 px top bar, centered message measure
  capped at 820 px.
- Tablet: persistent sidebar may narrow to 256 px.
- Mobile: sidebar becomes a modal drawer with a 50% scrim. The app uses `100dvh`,
  honors top and bottom safe areas, and never scrolls horizontally.
- The composer shares the message column width, remains above the mobile gesture
  area, and reserves enough scroll padding that the last answer is never hidden.
- Model and reasoning controls stay visible in the top bar. On narrow screens
  labels truncate, while the reasoning control retains a visible value.

## Interaction and accessibility

- Every interactive target is at least 44 × 44 px, with 8 px separation where
  adjacent actions could be confused.
- Use semantic `button`, `nav`, `main`, `header`, `aside`, `form` and `label`
  elements. Do not nest buttons inside a clickable container.
- Icon-only buttons require descriptive `aria-label` text and visible focus rings.
- Streaming status uses `role=status`; errors use `role=alert`; the conversation
  feed uses `role=log` with polite live updates.
- Streaming follows the latest token only while the reader remains near the
  bottom. Wheel/touch intent to inspect earlier content pauses following and
  exposes a 44 px "jump to latest" control; never scroll-jack the reader.
- Before first text, show truthful system stages (sending, context preparation,
  waiting for model, reasoning, search, image generation, composing) with a
  tabular elapsed-seconds counter. Never invent chain-of-thought.
- Hover is enhancement only. Pressed, disabled, loading and selected states must
  remain clear on touch devices.
- Motion is limited to 160–240 ms opacity/transform transitions and is disabled
  under `prefers-reduced-motion`.

## Core components

- Sidebar items: single-line title, clear active rail/surface, actions exposed on
  focus as well as hover.
- Model picker: searchable sheet/popover with capability descriptions and a
  visible selected check.
- Model picker descriptions lead with product positioning rather than a raw
  capability dump: GPT 5.6 is explicitly ordered Sol > Terra > Luna, while Grok
  models are described within their own family. Technical IDs remain secondary.
- Reasoning picker: visually paired with the model picker. It exposes three
  plain-language levels (low/medium/high), includes a short speed/quality
  description, and discloses the CPA `medium`/`high`/`max` mapping.
- Conversation lifecycle: pin is immediately visible; temporary retention is
  named as a seven-day recoverable state rather than a permanent archive.
- Storage: the sidebar shows the signed-in user's active 3 GB allowance and
  active/pinned conversation counts without turning the chat into a dashboard.
- Administrator view: cross-user conversations show their owner and a persistent
  read-only banner; mutation controls and the composer are disabled.
- Messages: user text on a subtle blue surface; assistant responses on the canvas
  without a heavy bubble. Tool calls are compact status cards, not oversized
  panels.
- Web/tool cards use localized action labels, display the actual allowlisted
  query/page/pattern, and link up to five sanitized result sources. Raw JSON and
  the ambiguous "security parameters" disclosure are not user-facing.
- Composer: solid white surface, blue focus ring, explicit upload and image-mode
  actions, 44 px send/stop control, and nearby recoverable error feedback.
- Empty chats draw three non-repeating suggestions from a 30-item pool and offer
  an explicit refresh control; image suggestions appear only for capable models.
- "Remember password" delegates storage to the browser credential manager. Only
  the preference and username may be retained in local storage.
- Mobile dialogs: bottom sheets with safe-area padding and an explicit close
  button; desktop dialogs remain centered.
