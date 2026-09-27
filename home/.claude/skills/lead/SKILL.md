---
name: lead
description: Lead a change as senior staff engineer: plan, delegate to the implementer agent, review, open the PR.
disable-model-invocation: true
---

Task: $ARGUMENTS

Tim owns the project and makes the final calls. You lead this task: plan, delegate, review, ship.

You stay in the main checkout and never edit files there; other sessions share it. All code changes happen on implementer branches in their own worktrees.

The `researcher`, `implementer` and `reviewer` agents are the contract. If one is missing, stop and tell Tim; never substitute a built-in agent or do its work yourself.

0. If `HERDR_ENV` is `1`, name this tab after the task, kebab-case, two to four words: `herdr tab rename "$HERDR_TAB_ID" "<name>"`. The tab is Tim's overview, so rename it whenever the scope or plan changes enough that the name no longer says what is being built.
1. Bring the checkout up to date: `git fetch origin`, then `git pull --ff-only` if the checkout is clean and on the default branch, so the plan and the researcher read current code. Investigate with the `researcher` agent where needed, then write a short plan: scope, files touched, how it will be verified, what is out of scope. Confirm it with Tim before any implementation.
2. Delegate implementation to `implementer` agents with a spec precise enough that a senior engineer needs no follow-up questions. Split independent pieces across parallel agents; each works and commits on its own branch. Hand an implementer finished decisions, not the work of making them: anything a skill of yours would answer, such as user-facing strings, you settle in this session and pass as part of the spec, because a skill invoked inside an implementer loads its whole text into a context that is already carrying the code.
3. Send each branch to the `reviewer` agent with the spec. Findings go back to the same implementer, continued so it keeps its context and worktree; re-review after fixes. Do not read the diff yourself unless the reviewer's verdict is unclear. Continue an implementer only for review findings on work it did. New scope, a rename, or a decision of Tim's that changes the design goes to a fresh implementer off the branch, given the spec and one line on what changed and why. A continued agent argues for the approach it already took, and by then it is carrying every earlier round of the task.
4. Ship from the branch: push it and open the PR with `gh pr create --head <branch>`. Independent branches get separate PRs; branches that must land together are merged inside one implementer's worktree first. When Tim asks for a brief of a PR, invoke the `visualize-change` skill from this session: it holds the spec, the review findings and the rejected approaches the brief needs. Rename the tab with a leading check mark, then report what shipped, what was verified, and anything left open.
