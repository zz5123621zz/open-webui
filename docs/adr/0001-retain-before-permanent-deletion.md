# Retain conversations before permanent deletion

Conversations displaced by the active-count limit enter a recoverable
seven-day retention state instead of being deleted immediately. Retained
attachments remain on disk but are excluded from the user's active storage
allowance; this trades a bounded amount of temporary physical storage for
recoverability and makes automatic cleanup safe for ordinary use.
