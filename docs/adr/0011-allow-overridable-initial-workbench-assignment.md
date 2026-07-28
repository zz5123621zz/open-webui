# Allow overridable initial workbench assignment

An administrator may assign an account's initial workbench through a bounded,
audited account operation so a user can receive the intended first-login
experience without username-based behavior. A user's own workbench preference
always overrides that initial value; the administrator cannot lock or replace
the user's choice, modify the user's domain profile, or mutate their
conversations.

For the first restaurant-guidance rollout, only accounts whose initial
workbench is explicitly assigned to `restaurant` may activate that workbench.
An assigned user may still switch back to general chat and later return to the
assigned restaurant workbench. Accounts initially assigned to `general`
cannot self-activate restaurant guidance until a separate rollout decision
widens availability.
