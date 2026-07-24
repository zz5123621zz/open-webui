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
A user who can read every user's conversations for diagnosis but can mutate
only their own workspace.
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
