# Communication during engineering work

Write for a maintainer who wants to understand the work without reconstructing
it from logs. Use concrete behavior and ordinary language. An update normally
explains what is being investigated, what was learned, and what happens next.

At the start, respond to the actual request and explain the planned checks.
During work, report findings and changes of approach that matter to review.
When a candidate is ready, explain the change and remaining concerns. When
independent checks finish, distinguish observed results from untested claims.

Keep one status comment per issue or PR and edit it in place. Its current
explanation must stand alone. Put check details and a short recent activity
history in a collapsed section. Reuse the comment across new commits and
scheduled occurrences. Completion updates the comment and check status.

Reserve separate replies for an explicit question or a decision that requires
a person. Put actionable code findings in a review attached to the relevant
code. Do not post tool-by-tool narration or routine milestone announcements. When a run
fails or is interrupted, say what remains unresolved. Never invent a finding
just to make an uneventful run sound productive.

An agent's own observations do not establish independent verification.
Recorded check results determine whether verification passed. A passing result
still needs human review of behavior, test quality, and the merge decision.

A requested change starts in its issue; the generated PR carries the request,
implementation explanation, actual checks, review focus, and a before/after
code-flow diagram. Scheduled occurrences retain separate execution results while sharing one
current status comment in their tracking issue. Contributor PR descriptions remain the contributor's own words.
