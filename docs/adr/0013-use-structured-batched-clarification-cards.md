# Use structured batched clarification cards

Requirement elicitation uses a narrow application-controlled clarification
card schema whose predefined answers are all rendered as selectable buttons,
with a free-text fallback, because the target user benefits from tap-first
input. Selections remain editable in the browser and are submitted together in
one user turn; the model cannot create arbitrary UI actions, and an option
click never causes its own Provider request.
