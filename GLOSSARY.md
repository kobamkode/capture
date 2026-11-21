# Capture

The `capture` CLI context: appending quick text captures to dated Obsidian daily notes.

## Language

**Capture**:
One CLI invocation's text together with its type (note or task).
_Avoid_: entry, snippet, memo

**Note**:
A capture type that records a thought; its rendered line carries a capture timestamp.
_Avoid_: thought, remark

**Task**:
A capture type that records an action item; its rendered line is an unchecked checkbox followed by the same timestamp, id and text columns a Note carries.
_Avoid_: todo, to-do, action item

**Daily note**:
The dated markdown file in the Obsidian vault that accumulates every capture for one calendar day.
_Avoid_: journal, daily file, log

**Capture id**:
The seven hex characters between pipes in a rendered line (`- 09:15 | 0b17a3b | text`), the shape of a git short hash, greppable across the vault so one capture can be called back later.
_Avoid_: hash, uuid, key
