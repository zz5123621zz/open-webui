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
explicitly bounded set of audited service settings, assign an overridable
initial workbench, and otherwise mutate only their own chat workspace.
_Avoid_: Superuser, owner

**Active conversation**:
A conversation currently counted toward the user's 30-conversation limit and
storage allowance.
_Avoid_: Live chat

**Pinned conversation**:
An active conversation protected from automatic retention. A user may pin at
most ten conversations.
_Avoid_: Favorite, starred chat

**Restaurant guidance**:
A restaurant-workbench conversation policy that applies requirement
elicitation to vague restaurant tasks in an ordinary conversation.
_Avoid_: Business diagnosis, fixed workflow classifier

**Requirement elicitation**:
A bounded dialogue that collects the missing goal, context, constraints, and
desired output needed to answer the user's current task precisely.
_Avoid_: Prompt tutoring, business diagnosis, interrogation

**Material ambiguity**:
Missing task information for which plausible alternatives would produce
meaningfully different valid answers.
_Avoid_: Short prompt, missing optional detail

**Delegated default**:
A visible task-specific assumption selected by the Agent after the user
explicitly delegates an unresolved choice.
_Avoid_: Inferred preference, restaurant profile fact

**Clarification round**:
One Agent response containing at most three high-impact questions before the
user may answer, generate immediately, or continue refining the request.
_Avoid_: Questionnaire page, one question per response

**Clarification card**:
A user-visible structured question whose predefined answers are all
application-controlled selectable choices, with a free-text fallback, produced
within a clarification round.
_Avoid_: Plain numbered list, arbitrary model-generated UI

**Clarification submission**:
A single user turn that submits all reviewed selections and free-text answers
from one clarification round, optionally ending further elicitation.
_Avoid_: Option click, automatic submission, multiple provider requests

**Task brief**:
A user-visible summary of the confirmed goal, relevant context, constraints,
and desired output that the Agent will use for the current answer.
_Avoid_: Hidden prompt, restaurant profile, conversation summary

**Restaurant profile**:
A user-owned, explicitly maintained set of stable restaurant facts that may
prefill future task briefs and is maintained only through confirmed in-task
updates, never through mandatory setup or silent learning.
_Avoid_: Automatic memory, daily operating data, administrator profile

**Profile update proposal**:
A user-visible in-task suggestion to save, replace, or delete one stable
restaurant fact, which has no long-term effect until the user explicitly
accepts it.
_Avoid_: Automatic profile update, task-specific override

**Brief confirmation**:
An explicit user action approving the current task brief for full answer
generation, while retaining the choices to add context or answer immediately.
_Avoid_: Implicit model readiness, mandatory questionnaire completion

**Brief readiness**:
The point at which the high-impact unknowns for the current task have been
resolved well enough for the Agent to produce the requested answer.
_Avoid_: Perfect information, mandatory form completion

**Initial workbench**:
An administrator-assigned account default that determines the user's starting
experience until the user chooses their own workbench.
_Avoid_: Locked workbench, username-based behavior

**Workbench preference**:
A user's persistent workbench choice, which overrides the initial workbench
without allowing an administrator to lock or replace that choice.
_Avoid_: Administrator preference, service setting

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

**Messaging bridge**:
An authenticated server-to-server boundary that lets an external messaging
gateway use a User's La4RainGPT restaurant conversation without sharing that
User's browser session.
_Avoid_: Browser API proxy, shared Agent account

**Bridge credential**:
A revocable, single-User secret that authorizes one messaging profile to use
the restaurant messaging bridge.
_Avoid_: User password, global integration key

**Bridge session**:
One external messaging session mapped to one active La4RainGPT conversation
for the User bound to its Bridge credential.
_Avoid_: WeChat account, Hermes profile, browser session

**WeChat clarification round**:
A Clarification round rendered as exactly three numbered plain-text questions,
each with locally assigned letter choices, for a WeChat Bridge session.
_Avoid_: WeChat card, Mini Program form

**Playable audio attachment**:
One of the ordered WAV files generated from a completed answer and sent after
the answer text through the messaging gateway.
_Avoid_: Native voice bubble, guaranteed autoplay
