# capture

Append thoughts and tasks to your Obsidian daily note straight from the shell.

```console
$ capture i think i need to learn math
$ capture -t go to dentist
$ cat ideas.txt | capture
```

Each run appends to today's daily note in your vault, creating it when missing. When the date rolls over, the next capture starts the new day's file. Nothing else runs in the background — every invocation is stateless.

## Install

```sh
go install github.com/kobamkode/capture@latest
```

or build from source:

```sh
git clone https://github.com/kobamkode/capture
cd capture && go build -o capture .
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

The Go layout syntax is what `time.Format` expects: `2006-01-02` is the default, `2006-01-02 Mon` adds the weekday. strftime (`%Y-%m-%d`) is not supported.

## Usage

```sh
capture <text...>            # note entry:  - 09:15 | text
capture -t <text...>         # task entry:  - [ ] text
capture --todo <text...>     # same as -t
capture -- -todo list        # text starting with a dash
printf 'a\nb\n' | capture    # one entry per non-empty line
```

Details:

- Quotes are optional: `capture i think i need to learn math` is one entry (all arguments joined with single spaces).
- Note entries carry a 24-hour local timestamp; task entries are unchecked checkboxes without one.
- Stdin and arguments cannot be combined — that is an error, as is an empty capture.
- Flags come before the text; a dash-word after the first word is captured as text.
- Success prints nothing and exits 0. Every error goes to stderr and exits 1.

## Example output

```markdown
- 09:15 | i think i need to learn math
- [ ] go to dentist
- 09:20 | remember the milk
```

## Development

```sh
go test ./...
```

Tests drive a single `run` seam with an injected clock and temp vault directories, so date rollover and timestamps assert deterministically.
