# La4RainGPT

La4RainGPT is a private, multi-user AI chat service. It keeps each regular
user's workspace isolated while allowing an administrator to diagnose the
service through read-only cross-user visibility.

## Language

**User**:
A person with an isolated chat workspace, storage allowance, and conversation
limits.
_Avoid_: Tenant, member

**Administrator**:
A user who can read every user's conversations for diagnosis, manage an
explicitly bounded set of audited service settings, and mutate only their own
chat workspace.
_Avoid_: Superuser, owner

**Active conversation**:
A conversation currently counted toward the user's 30-conversation limit and
storage allowance.
_Avoid_: Live chat

**Pinned conversation**:
An active conversation protected from automatic retention. A user may pin at
most ten conversations.
_Avoid_: Favorite, starred chat

**Temporary retention**:
The seven-day recoverable state for a conversation removed from the active
workspace by the user or by automatic limit enforcement.
_Avoid_: Permanent archive, trash

**Permanent deletion**:
The irreversible removal of a retained conversation and its stored
attachments after its retention period.
_Avoid_: Cleanup

**Storage allowance**:
The three-gigabyte attachment capacity assigned to each user's active
workspace. Temporarily retained attachments do not consume this allowance.
_Avoid_: Disk size, account quota

**Safe reasoning summary**:
A provider-authored, user-visible summary of the model's approach that excludes
private raw chain-of-thought.
_Avoid_: Chain-of-thought, raw reasoning, thought transcript

**Activity status**:
A factual description of observable work such as waiting, searching, or
generating that does not claim access to unreported model reasoning.
_Avoid_: Synthetic thinking, inferred reasoning

**Progressive summary delivery**:
A provider capability that delivers completed safe reasoning-summary sections
while the response is still reasoning.
_Avoid_: Live chain-of-thought, continuous thinking

**Service setting**:
A globally effective operating choice selected by an administrator from a
fixed application-defined set; it contains neither credentials nor arbitrary
provider configuration.
_Avoid_: Admin preference, environment variable, provider JSON

**Configured summary mode**:
The administrator's persistent choice to allow automatic progressive summary
delivery or to disable it.
_Avoid_: Effective summary state

**Effective summary state**:
The currently observed availability of progressive summary delivery after
provider compatibility checks and fallback.
_Avoid_: Configured summary mode

**Accepted provider attempt**:
A provider request that has returned a successful HTTP response or emitted any
stream event. It may already have consumed quota or caused tool side effects.
_Avoid_: Completed response, safe-to-retry request

**Response job**:
An accepted request whose provider work belongs to La4RainGPT and continues
independently of browser connections until completion, explicit cancellation,
or service interruption.
_Avoid_: SSE request, browser request

**Response subscription**:
A temporary viewer connection that receives events from a response job without
owning or controlling that job's lifetime.
_Avoid_: Response job, generation request

**Explicit response cancellation**:
An authenticated user action that ends their running response job. Closing a
tab, losing connectivity, signing out, or ending a subscription is not
cancellation.
_Avoid_: Client disconnect, stop watching

**Service-interrupted response**:
A response job that could not finish because the application stopped or
restarted. Its received durable evidence remains visible and retry is manual.
_Avoid_: Automatically resumed response, provider retry

**Transparent compatibility fallback**:
A single application-initiated retry without progressive summary delivery
after the provider explicitly rejects that field before accepting the first
attempt.
_Avoid_: Stream recovery, automatic answer retry

**Compatibility cooldown**:
The 30-minute, provider-endpoint-and-model-scoped period after an explicit
progressive-delivery rejection during which new requests use baseline summary
delivery. It changes effective state without changing configured summary mode.
_Avoid_: Automatic disable, administrator setting

**Compatibility probe**:
The next eligible normal chat request allowed to retest progressive summary
delivery after a compatibility cooldown expires or an administrator clears it.
Concurrent requests continue with baseline delivery while the probe is running.
_Avoid_: Health check, answer retry

**Durable response evidence**:
Provider-authored reasoning-summary sections and factual search or tool events
stored with their source identity and order so conversation history can replay
what was actually received.
_Avoid_: Thought transcript, reconstructed progress

**Transient activity indicator**:
A live-only timer or factual waiting state shown while a response is running.
It is neither message content nor conversation history.
_Avoid_: Durable response evidence, reasoning summary

**Sanitized web evidence**:
A provider-reported search query, page title, or HTTP(S) URL made safe before
display and storage. URL credentials, fragments, tracking parameters, and
secret-like parameters are removed while ordinary navigation parameters remain.
_Avoid_: Raw provider action, request metadata, browsing log

**Setting activation boundary**:
The start of a new response job. Service-setting changes are snapshotted for
jobs started after the change and never cancel or rewrite a job already in
progress.
_Avoid_: Immediate stream mutation, response cancellation

**Manual compatibility recheck**:
An administrator action that clears compatibility cooldown so the next eligible
normal chat becomes the compatibility probe. The action itself sends no model
request and consumes no provider quota.
_Avoid_: Active health check, test prompt

**Observed event latency**:
The time from La4RainGPT receiving a provider summary, search, or tool event to
that event being rendered in the browser. Application processing targets no
more than one second under validation conditions.
_Avoid_: First semantic progress time, total response time

**First semantic progress time**:
The provider-controlled time until it emits the first safe reasoning summary,
search, tool, or answer event. La4RainGPT reports elapsed time but cannot
guarantee a maximum.
_Avoid_: Observed event latency, first-token transport latency

**Speech provider**:
An administrator-selected external service that converts committed answer text
into audio without participating in answer generation.
_Avoid_: Chat provider, voice model

**Speech session**:
A user-owned, short-lived stream that accepts ordered answer-text segments and
returns playable audio while a listener is present.
_Avoid_: Response job, stored recording

**Spoken answer text**:
User-visible assistant answer text selected for speech after markup and
non-speech content are removed. Reasoning summaries and tool activity are never
spoken answer text.
_Avoid_: Raw response stream, chain-of-thought

**Automatic reading preference**:
A user's persistent choice to start a speech session when an answer begins.
It is disabled by default and does not itself grant a browser permission to
play audio.
_Avoid_: Autoplay permission, service setting

**Manual reading**:
An explicit user action that starts speech for visible answer text regardless
of the user's automatic reading preference.
_Avoid_: Automatic reading

**Speech activation boundary**:
The start of a new speech session. Speech service-setting changes affect new
sessions and never reconfigure or interrupt an existing session.
_Avoid_: Response activation boundary, live provider mutation
