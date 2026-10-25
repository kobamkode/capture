# capture

Append thoughts and tasks to your Obsidian daily note straight from the shell.

```console
$ capture i think i need to learn math
$ capture -t go to dentist
$ cat ideas.txt | capture
```

Each run appends to today's daily note in your vault, creating it when missing. When the date rolls over, the next capture starts the new day's file. Nothing else runs in the background — every invocation is stateless.

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
capture <text...>            # note entry:  - 09:15 | text
capture -t <text...>         # task entry:  - [ ] text
capture --todo <text...>     # same as -t
capture -- -todo list        # text starting with a dash
printf 'a\nb\n' | capture    # one entry per non-empty line
capture -l                   # print today's captures
capture --list               # same as -l
capture -l -w                # print the past week's captures under day headings
capture --list --week        # same as -l -w
capture --version            # print version and exit
```

Details:

- Quotes are optional: `capture i think i need to learn math` is one entry (all arguments joined with single spaces).
- Note entries carry a 24-hour local timestamp; task entries are unchecked checkboxes without one.
- Stdin and arguments cannot be combined — that is an error, as is an empty capture.
- `-l` / `--list` prints today's captures: each capture-shaped line of today's daily note, verbatim in file order. An empty or missing day prints nothing and exits 0. It takes no text, no stdin, and no `-t`.
- `-w` / `--week` extends `-l` to the past week: the last seven daily notes, oldest day first, each day under a `## YYYY-MM-DD` heading. Days with no captures are skipped. It requires `-l` and accepts no text, no stdin, and no `-t`.
- Flags come before the text; a dash-word after the first word is captured as text.
- A successful capture prints nothing and exits 0. Every error goes to stderr and exits 1.

## Example output

```markdown
- 09:15 | i think i need to learn math
- [ ] go to dentist
- 09:20 | remember the milk
```

Listing the same day back — only the captures, in file order:

```console
$ capture -l
- 09:15 | i think i need to learn math
- [ ] go to dentist
- 09:20 | remember the milk
```

On a day with nothing captured the listing is empty and the exit code is 0:

```console
$ capture -l
```

The week listing adds a heading per day, oldest day first:

```console
$ capture -l -w
## 2026-10-08
- [ ] old task

## 2026-10-13
- 08:00 | yesterday note
- [ ] dentist tomorrow

## 2026-10-14
- 09:15 | today note
```

## Development

```sh
go test ./...
```

Tests drive a single `run` seam with an injected clock and temp vault directories, so date rollover and timestamps assert deterministically.

Releases are git tags: `go install github.com/kobamkode/capture@latest` resolves to the newest tag, and `--version` reports it because `go install pkg@vX.Y.Z` embeds the module version in the binary. Tag with `git tag vX.Y.Z && git push --tags`; a build without a tag reports `devel`.
