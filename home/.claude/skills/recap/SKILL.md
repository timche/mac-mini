---
name: recap
description: "Catch Tim up on what happened in this session since his last message, and on everything still open or waiting on him. Load when he asks for a recap, a summary, what happened, where things stand, or what he missed while away."
---

Since: $ARGUMENTS (if this is blank, since Tim's last message before this one)

Tim was away while this session worked and did not follow it. Catch him up from the transcript, then check the present state, since the transcript says what was true when each step ran, not now. If the conversation was summarized in that span, say so and work from the summary.

## Check before you write

- Background tasks, agents and workflows you started: which finished, which are still running, which failed or were stopped.
- Every repository you touched: branch, uncommitted changes, unpushed commits (`git status`, `git log @{u}..`).
- Every pull request you opened or pushed to: state, CI and review (`gh pr view <n> --json state,statusCheckRollup,reviewDecision`).
- Dev servers, browsers, windows, VMs and other processes you started that are still up.
- Artifacts you published, and comments left on them since.

Run these checks; do not report one from memory.

## Write it

Lead with one or two sentences: where the work stands now against what Tim last asked for.

**Waiting on you**, numbered so he can answer item by item: each question, decision, approval, login, credential or device only he can supply, with what you recommend and why it blocks. Write "Nothing" when there is nothing. This section comes first after the lead because it is what he came back for.

**What happened**: the outcomes in order, not every step. A decision you made on his behalf, an approach you dropped and why, a surprise, a failure and what you did about it. Each pull request as `#<id> (<url>)`, each file by its absolute path, each artifact by its link.

**Still open**: what is running, uncommitted, unpushed, unmerged or unfinished, and what happens next if he says nothing.

Keep it short enough to read in a minute. Leave out what went as expected and changed nothing; give detail only where he will act on it. Do not resume the work or start anything new in this reply.
