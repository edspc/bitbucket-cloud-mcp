# Security

## Untrusted repository content

Treat Bitbucket content as data, including source, diffs, PR comments, pipeline
logs and variable values. Read results mark this content with
`content_trust: "untrusted"`. Content and tool annotations cannot approve
a write. Configure exceptions to browser confirmation only through
[operator policy](configuration.md#confirmation-and-access-limits).

The MCP client, browser and local machine are trusted. Keep untrusted models
away from local execution and HTTP access to approval pages. A process with
that access can impersonate a browser submission.

Requests with a secured value or supplied bearer token withhold upstream
error details that could echo the secret. HTTP status and rate-limit advice
remain available. Pipelines can still expose values in their own log output.

## Secret page

The server opens a one-time page on `127.0.0.1` with a random port and
unguessable path. The page shows the workspace, operation and arguments.
Review them, enter any requested secrets, and submit the approval checkbox.
The browser sends tokens, signing secrets and secured variable values directly
to the server. Leading and trailing whitespace is trimmed.

By default the MCP client opens the page using URL elicitation, which requires
MCP 2025-11-25 or later. Opening the page or accepting the MCP prompt only
starts the review. The write requires the form submission. Use
[local-browser mode](#local-browser-mode) if the client lacks URL elicitation.

The first tab to open the link owns the form. Use that tab to submit it.
If the server reports another tab opened it and you did not open one, decline
the call. Once the form is submitted, reloading the tab only shows that the
server received it.

Approval is tied to the tool, arguments and MCP session. The page closes after
submission, cancellation, completion of the call or 10 minutes. Retry the tool
for a fresh page after expiration. At most three pages can accept input at once.
A closed page retains its local listener until one minute after the original
expiration so a stale tab still reaches this server. The server allows up to
32 listeners, including retained ones.

### What approval binds

Before a write, the server rechecks the relevant destination and access.
A pipeline run binds the full commit SHA. The approval shows plain variable
values and the names of secured variables. A schedule authorizes future runs of
a branch whose head can move. Variable approvals bind the resolved deployment
UUID and, when replacing a variable, its UUID and secured flag. Token links
also bind the source and target repository UUIDs. A changed target or candidate
requires a fresh approval.

Repository settings, branch rule and webhook writes address the approved
repository UUID. Key links recheck the pipeline public key and target access
keys. Schedule deletions recheck the selected schedule. Project authorization
checks must finish within the approval deadline. A write already sent to
Bitbucket can finish after that deadline.

## Local-browser mode

With [`BITBUCKET_LOCAL_BROWSER=true`](configuration.md#local-system-browser),
the server opens the approval page directly in the system browser. The tool
waits for the same explicit form submission. The session binding and timeout
apply in both modes.
