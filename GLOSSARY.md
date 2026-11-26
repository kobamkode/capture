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

**Previous daily note**:
The most recent **Daily note** dated before today, whatever its date — the source a **Move** drains. Days without a note simply do not exist for it.
_Avoid_: last file, yesterday's note, latest note

**Move**:
The act of taking the unfinished **Tasks** of the **Previous daily note** into today's **Daily note**, where each arrives as the same Task: same id, same capture time, same text.
_Avoid_: rollover, carry-over, migration, transfer

**Capture id**:
The seven hex characters between pipes in a rendered line (`- 09:15 | 0b17a3b | text`), the shape of a git short hash, unique within a Daily note; `capture find <id>` calls the capture back.
_Avoid_: hash, uuid, key
