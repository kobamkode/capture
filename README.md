# capture

Append thoughts and tasks to your Obsidian daily note straight from the shell.

```console
$ capture i think i need to learn math
$ capture -t go to dentist
$ cat ideas.txt | capture
$ capture open
```

Each run appends to today's daily note in your vault, creating it when missing. When the date rolls over, the next capture starts the new day's file. `capture open` jumps straight to the same note in Obsidian. Nothing else runs in the background — every invocation is stateless.

## Install

Recommended — needs a working Go toolchain:

```sh
go install github.com/kobamkode/capture@latest
```

or build from source:

```sh
git clone https://github.com/kobamkode/capture
cd capture && go build -o capture .
```

Confirm what you have:

```console
$ capture --version
capture v0.2.0
```

## Update

Re-run the install command; `go install` fetches the newest tagged version:

```sh
go install github.com/kobamkode/capture@latest
```

## Configuration

`capture` reads plain environment variables:

| Variable | Required | Meaning |
| --- | --- | --- |
| `CAPTURE_VAULT` | yes | Vault root. Fails when unset or not an existing directory. |
| `CAPTURE_DAILY_DIR` | no | Folder inside the vault for daily notes. Unset = vault root. Set but missing = fail (never auto-created). |
| `CAPTURE_DAILY_FORMAT` | no | Daily note filename as a Go time layout. Default `2006-01-02`. |

Copy the template and load it into your shell:

```sh
cp .env.example .env   # edit values
source .env
```

Or keep the values in an alias in your shell rc (`~/.bashrc`, `~/.zshrc`, …) so `capture` works in any shell without sourcing a file — fill in your paths:

```sh
alias c="CAPTURE_VAULT= CAPTURE_DAILY_DIR= capture"
```

The Go layout syntax is what `time.Format` expects: `2006-01-02` is the default, `2006-01-02 Mon` adds the weekday. strftime (`%Y-%m-%d`) is not supported.

## Usage

```sh
capture <text...>            # note entry:  - 09:15 | 0b17a3b | text
capture -t <text...>         # task entry:  - [ ] | 09:15 | 0b17a3b | text
capture --todo <text...>     # same as -t
capture -- -todo list        # text starting with a dash
printf 'a\nb\n' | capture    # one entry per non-empty line
capture -l                   # print today's captures
capture --list               # same as -l
capture -l -w                # print the past week's captures under day headings
capture --list --week        # same as -l -w
capture open                 # open today's daily note in Obsidian
capture find <id>            # print every capture with that id: path:line:text
capture ask <id> [instr...]  # hand a capture to the pi agent, then offer to close the task
capture move                 # move the previous daily note's unfinished tasks into today's
capture -- open              # capture the word "open"
capture -- find <text>       # capture the word "find"
capture -- move              # capture the word "move"
capture --version            # print version and exit
```

Details:

- Quotes are optional: `capture i think i need to learn math` is one entry (all arguments joined with single spaces).
- Note and task entries carry a 24-hour local timestamp; task entries are unchecked checkboxes.
- Every rendered line carries a **capture id** column: seven hex characters between pipes, the shape of a git short hash — after the timestamp of a note or a task. Call a capture back with `capture find <id>`, or tell an agent "explore `0b17a3b` in journal".
- Stdin and arguments cannot be combined — that is an error, as is an empty capture.
- `-l` / `--list` prints today's captures: each capture-shaped line of today's daily note, verbatim in file order. An empty or missing day prints nothing and exits 0. It takes no text, no stdin, and no `-t`.
- `-w` / `--week` extends `-l` to the past week: the last seven daily notes, oldest day first, each day under a `## YYYY-MM-DD` heading. Days with no captures are skipped. It requires `-l` and accepts no text, no stdin, and no `-t`.
- `open` opens today's daily note in the Obsidian app, creating an empty note first when the day has none. It takes no flags, text, or stdin; use `capture -- open` to capture the word itself. It uses the same `CAPTURE_VAULT`, `CAPTURE_DAILY_DIR`, and `CAPTURE_DAILY_FORMAT` as a capture.
- `find <id>` prints every capture whose id column equals `<id>` as `path:line:text`, oldest daily note first. It reads the daily notes in the configured directory across all days, ignores files whose name is not a daily note under `CAPTURE_DAILY_FORMAT`, and never writes. It takes exactly one id, no stdin, and no flags; no match prints nothing and exits 0, a malformed id is an error, and `capture -- find <text>` still captures the word.
- `ask <id> [instruction...]` calls a capture back into the `pi` agent: the capture's text, then the remaining words as the instruction (default: explain the capture and what it was about), are handed to `pi`, which inherits the terminal, so the session stays interactive. The first hit in daily-note order is used, and an id with no hit is an error that starts no session. When the hit is an open task and `pi` exits 0, it asks `mark it done? [y/N]` on stderr and flips that line's box in the daily note on `y` only — addressing the line by number but touching it only while it still holds this id. `n`, no answer, or a failed `pi` leaves the task open. It takes no flags; stdin is not capture input, it is `pi`'s terminal input and the closing answer, so `capture -- ask <text>` still captures the word.
- `move` is the morning step: it finds the previous daily note — the newest daily note dated before today, however old — and moves its unfinished tasks into today's, verbatim: same `- [ ] ` prefix, same capture time, same capture id, so `capture find <id>` still calls them back. Each moved line is deleted from the source, whose other bytes (trailing newline included) are left alone; a source the move empties stays as an empty file. Checked tasks, notes, prose, headings and nested checkboxes never move. A second run is a no-op, and nothing to move prints nothing, exits 0 and creates no file for today. It takes no flags, text, or stdin, so `capture -- move` still captures the word.
- Flags come before the text; a dash-word after the first word is captured as text.
- A successful capture prints nothing and exits 0. Every error goes to stderr and exits 1.

## Example output

```markdown
- 09:15 | 0b17a3b | i think i need to learn math
- [ ] | 09:16 | b7e4f21 | go to dentist
- 09:20 | 3c9d0ae | remember the milk
```

Listing the same day back — only the captures, in file order:

```console
$ capture -l
- 09:15 | 0b17a3b | i think i need to learn math
- [ ] | 09:16 | b7e4f21 | go to dentist
- 09:20 | 3c9d0ae | remember the milk
```

On a day with nothing captured the listing is empty and the exit code is 0:

```console
$ capture -l
```

The week listing adds a heading per day, oldest day first:

```console
$ capture -l -w
## 2026-10-08
- [ ] | 07:30 | 5a1c8b3 | old task

## 2026-10-13
- 08:00 | e02f7d9 | yesterday note
- [ ] | 18:30 | 1d3b6a4 | dentist tomorrow

## 2026-10-14
- 09:15 | c48e5f0 | today note
```

Moving the previous daily note's unfinished tasks into today prints each line
that left the old note:

```console
$ capture move
- [ ] | 08:10 | 0badf00 | call the dentist
- [ ] | 18:30 | 1d3b6a4 | dentist tomorrow
```

## Development

```sh
go test ./...
```

Tests drive a single `run` seam with an injected clock and temp vault directories, so date rollover and timestamps assert deterministically.

Releases are git tags: `go install github.com/kobamkode/capture@latest` resolves to the newest tag, and `--version` reports it because `go install pkg@vX.Y.Z` embeds the module version in the binary. A build without a tag reports `devel`.

Tags are cut automatically: `.github/workflows/release-tag.yml` runs the tests on every push to `main`, then tags `HEAD` with the next `vX.Y.Z` from the subjects since the last tag. A `feat:` commit, or a breaking `!:` on any type, bumps the minor; everything else bumps the patch. Tags are annotated and never moved, and a push whose `HEAD` is already tagged is a no-op. To cut one by hand, `git tag -a vX.Y.Z -m "tag vX.Y.Z: ..." && git push origin vX.Y.Z`.
