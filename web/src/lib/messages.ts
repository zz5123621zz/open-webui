import type { Message } from './types';

// Joins a message's visible text parts. Shared by copy-to-clipboard and the
// edit prefill so the two can never drift apart.
export function messageText(message: Message): string {
  return message.parts
    .filter((part) => part.type === 'text')
    .map((part) => part.text || '')
    .join('\n\n');
}
