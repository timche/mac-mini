---
name: visualize-change
description: Explain the change this session is planning, doing or has shipped as a published page Tim reads before the diff, to understand it at a high level. Use only when Tim asks for it, never unprompted.
disable-model-invocation: true
---

Publish one page that gives Tim the high-level picture of the change before he reads the diff. This session already knows the change: the plan, the spec, what was rejected, what was verified. Write from that; read the diff or PR only for what the session does not know. An argument may name a branch or PR to explain instead.

The page answers four things, in whatever order and form suits the change:

- What changed and why, in a sentence or two.
- The mechanism: where state lives now, what the flow is, what was removed. Mechanism over file lists.
- What was kept or rejected, and why.
- What was verified and what is still open.

Every change is different, so the page is not the same layout every time. Load `show-me` to pick the smallest view that makes the point and `artifact-design` for the page. A change that moves state wants a before/after pair and a flow tagged kept, removed or new. A UI change wants two screenshots. A batch of small fixes wants a table. Tim reads on desktop and sometimes on his phone, so it must read well at both widths.

Publish with the Artifact tool, titled after the change, and print the link alongside the PR link if there is one. Nothing goes into the PR body.
