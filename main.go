package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// dailyFormat is the default Go time layout for the daily note filename.
const dailyFormat = "2006-01-02"

// The line shapes a capture renders, the single source for both the writer
// (entry in run) and the list matcher below. Both types carry the same
// time + colSep + id + colSep + text tail; only the opening column differs: a
// Note opens with notePrefix, a Task with a checkbox and taskTimePipe. Edit
// these and both sides follow.
const (
	notePrefix     = "- "
	colSep         = " | "
	noteTimeFormat = "15:04"
	taskPrefix     = "- [ ] "
	// taskTimePipe opens a Task's time column: taskPrefix already ends in a
	// space, so the pipe keeps the time from reading as part of the checkbox.
	taskTimePipe = "| "
)

// randomID is the capture id: seven hex characters, the shape of a git short
// hash, sitting between pipes as a column of its own. Collisions are left to
// chance: 28 random bits over one day's file make one vanishingly unlikely, and
// a dedupe scan would cost a re-read on every capture.
func randomID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:])[:7], nil
}

const helpText = `Usage: capture [flags] text...
       capture open
       echo "line" | capture

Appends each capture as an entry to today's daily note in an Obsidian vault.
A successful capture prints nothing (exit 0); -l writes the listing to stdout;
errors go to stderr (exit 1).

Commands:
  open
        open today's daily note in Obsidian, creating it when missing.
        Takes no flags, text, or stdin. To capture the word itself, use the
        -- separator: capture -- open.

Input rules:
  Arguments: all remaining words are joined with single spaces into one entry.
  Stdin: when piped, each non-empty line becomes its own entry (blank lines
  are skipped, lines are trimmed).
  Arguments and piped stdin together is an error; empty input is an error.
  Use -- before text that starts with a dash, e.g. capture -- -todo list.

Capture ids:
  Every line carries a capture id column: seven hex characters between pipes,
  e.g. "- 09:15 | 0b17a3b | text" or "- [ ] | 09:15 | 0b17a3b | value". Grep
  the vault for an id to call that capture back later.

Flags:
  -t, --todo
        record a task instead of a note (renders "- [ ] | HH:MM | id | text");
        without it the entry is a note (renders "- HH:MM | id | text").
  -l, --list
        print today's captures: each capture-shaped line of today's daily note
        verbatim, in file order. An empty or missing day prints nothing and
        exits 0. Takes no text, no stdin, and no -t.
  -w, --week
        with -l, print the past week's captures instead of today's: the last
        seven daily notes, oldest day first, each day under a "## YYYY-MM-DD"
        heading; days without captures are skipped. Requires -l and, like -l,
        takes no text, no stdin, and no -t.
  -version
        print the capture version and exit.

Environment:
  CAPTURE_VAULT
        Vault root directory. Required: capture fails when unset or not an
        existing directory. No default.
  CAPTURE_DAILY_DIR
        Subfolder inside the vault for daily notes. Unset: daily note lives at
        the vault root. Set but missing: fails (folders are never auto-created).
  CAPTURE_DAILY_FORMAT
        Daily note filename as a Go time layout, default ` + dailyFormat + `.
        Go layout syntax, NOT strftime: 2006-01-02 is correct, %Y-%m-%d fails.
`

func main() {
	if err := run(os.Args[1:], os.Getenv, time.Now(), os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the production entry point; tests needing to intercept the Obsidian
// launch call runWithOpener directly.
func run(args []string, getenv func(string) string, now time.Time, stdin io.Reader, stdout, stderr io.Writer) error {
	return runWithOpener(args, getenv, now, stdin, stdout, stderr, openURL)
}

// version reports the module version baked in by `go install pkg@vX.Y.Z`,
// so git tags drive releases with no constant to bump. Local builds have
// no module version and report "devel".
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
}

func runWithOpener(args []string, getenv func(string) string, now time.Time, stdin io.Reader, stdout, stderr io.Writer, open func(string) error) error {
	// `capture open` is a command, not a capture of the word "open". It must be
	// the first argument, so `capture -t open` and `capture -- open` still
	// capture the text.
	openMode := len(args) > 0 && args[0] == "open"
	if openMode {
		args = args[1:]
	}

	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var task bool
	flags.BoolVar(&task, "t", false, "record a task instead of a note")
	flags.BoolVar(&task, "todo", false, "record a task instead of a note")
	var list bool
	flags.BoolVar(&list, "l", false, "print today's captures and exit")
	flags.BoolVar(&list, "list", false, "print today's captures and exit")
	var week bool
	flags.BoolVar(&week, "w", false, "with --list, print the past week's captures and exit")
	flags.BoolVar(&week, "week", false, "with --list, print the past week's captures and exit")
	var showVersion bool
	flags.BoolVar(&showVersion, "version", false, "print the capture version and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			io.WriteString(stdout, helpText)
			return nil
		}
		return err
	}
	// Version wins before any stdin read or vault check: it must work with
	// no CAPTURE_VAULT set and must never block on a piped stdin.
	if showVersion {
		fmt.Fprintf(stdout, "capture %s\n", version())
		return nil
	}

	if openMode {
		// Open takes no configuration beyond the environment: reject flags and
		// text before reading stdin or touching the vault.
		if task || list || week || len(flags.Args()) > 0 {
			return errors.New("open takes no flags, text, or stdin")
		}
	} else if week && !list {
		// Week is a listing modifier: alone it is a mistyped command, so fail
		// loudly instead of capturing "-w text".
		return errors.New("week mode requires -l")
	}

	// A terminal stdin must not be read: io.ReadAll would block until EOF
	// (Ctrl-D), hanging `capture quick thought`. Detect a terminal via the
	// os.File method set and treat stdin as absent. Stat error or no Stat
	// method (pipes, plain readers) falls through to the read path.
	var raw []byte
	piped := false
	isTerminal := false
	if f, ok := stdin.(interface{ Stat() (os.FileInfo, error) }); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			isTerminal = true
		}
	}
	if !isTerminal {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
		raw = b
		piped = strings.TrimSpace(string(raw)) != ""
	}

	// List and open modes accept no capture input: a mistyped command must fail
	// loudly instead of silently dropping the input.
	text := strings.Join(flags.Args(), " ")
	if openMode {
		if piped {
			return errors.New("open takes no flags, text, or stdin")
		}
	} else if list {
		if text != "" || piped || task {
			return errors.New("list mode accepts no capture input")
		}
	} else {
		if piped && text != "" {
			return errors.New("arguments and stdin at once are ambiguous")
		}
		if !piped && text == "" {
			return errors.New("empty capture")
		}
	}

	vault := getenv("CAPTURE_VAULT")
	if vault == "" {
		return errors.New("CAPTURE_VAULT is not set")
	}
	info, err := os.Stat(vault)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("CAPTURE_VAULT is not a directory: %s", vault)
	}

	// CAPTURE_DAILY_DIR selects a subfolder that must already exist: a typo
	// must fail rather than fork the vault structure by auto-creating it.
	dir := vault
	if d := getenv("CAPTURE_DAILY_DIR"); d != "" {
		dir = filepath.Join(vault, d)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return fmt.Errorf("CAPTURE_DAILY_DIR is not a directory: %s", d)
		}
	}

	// CAPTURE_DAILY_FORMAT is a Go time layout. The formatted name must parse
	// back to today's date, so strftime-style garbage (e.g. %Y-%m-%d) fails
	// instead of silently writing one wrong file for every day.
	layout := getenv("CAPTURE_DAILY_FORMAT")
	if layout == "" {
		layout = dailyFormat
	}
	name := now.Format(layout)
	if parsed, err := time.Parse(layout, name); err != nil ||
		parsed.Year() != now.Year() || parsed.Month() != now.Month() || parsed.Day() != now.Day() {
		return fmt.Errorf("CAPTURE_DAILY_FORMAT is not a valid Go time layout: %s", layout)
	}

	path := filepath.Join(dir, name+".md")
	if openMode {
		if err := ensureNote(path); err != nil {
			return err
		}
		return open(obsidianURL(path))
	}
	if list {
		if week {
			return listWeek(dir, layout, now, stdout)
		}
		return listCaptures(path, stdout)
	}
	// Task entries render "- [ ] | HH:MM | id | text"; note entries render
	// "- HH:MM | id | text". Piped stdin yields one entry per non-empty
	// trimmed line; the task flag applies to stdin-derived entries too.
	entry := func(s string) (string, error) {
		id, err := randomID()
		if err != nil {
			return "", fmt.Errorf("generating capture id: %w", err)
		}
		prefix := notePrefix
		if task {
			prefix = taskPrefix + taskTimePipe
		}
		return prefix + now.Format(noteTimeFormat) + colSep + id + colSep + s + "\n", nil
	}
	var buf strings.Builder
	if piped {
		for _, l := range strings.Split(string(raw), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				e, err := entry(l)
				if err != nil {
					return err
				}
				buf.WriteString(e)
			}
		}
	} else {
		e, err := entry(text)
		if err != nil {
			return err
		}
		buf.WriteString(e)
	}
	line := buf.String()

	existing, err := os.ReadFile(path)
	if err == nil {
		if len(existing) > 0 && existing[len(existing)-1] != '\n' {
			line = "\n" + line
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, line)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// ensureNote creates an empty Daily note when it is missing, so opening the day
// never depends on Obsidian inventing the file. Existing notes are untouched.
func ensureNote(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

// obsidianURL builds the Obsidian URI that opens path. The absolute path form
// sidesteps vault display-name mismatches, and url.Values encodes spaces and
// non-ASCII characters.
func obsidianURL(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	q := url.Values{}
	q.Set("path", path)
	return "obsidian://open?" + q.Encode()
}

// openURL hands the URI to the desktop's registered obsidian:// handler.
func openURL(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("opening Obsidian: %w: %s", err, msg)
		}
		return fmt.Errorf("opening Obsidian: %w", err)
	}
	return nil
}

// noteMarker matches a Note line's prefix, built from the same notePrefix and
// colSep constants the writer uses; prose bullets and template headings never
// carry it. Ids are not required: a line captured before ids existed still
// matches.
var noteMarker = regexp.MustCompile("^" + regexp.QuoteMeta(notePrefix) + `\d{2}:\d{2}` + regexp.QuoteMeta(colSep))

// isCaptureLine reports whether a Daily note line is a stored capture: a Note
// or a Task checkbox (unchecked or checked; capture itself only ever writes
// taskPrefix, the checked states come back from vault edits).
func isCaptureLine(line string) bool {
	return noteMarker.MatchString(line) ||
		strings.HasPrefix(line, taskPrefix) ||
		strings.HasPrefix(line, "- [x] ") ||
		strings.HasPrefix(line, "- [X] ")
}

// captureLines returns the capture-shaped lines of one Daily note, verbatim
// in file order. A missing Daily note is an empty result: no error, nothing
// created.
func captureLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if isCaptureLine(line) {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// listCaptures prints today's capture-shaped lines verbatim in file order.
// A missing Daily note is an empty result: no output, no error, nothing created.
func listCaptures(path string, stdout io.Writer) error {
	lines, err := captureLines(path)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return nil
}

// listWeek prints the past seven days' captures, oldest day first, each day
// under a "## YYYY-MM-DD" heading; days without captures are skipped, so a
// missing Daily note is an empty result. Filenames follow the configured
// daily layout, headings use the canonical date.
func listWeek(dir, layout string, now time.Time, stdout io.Writer) error {
	for i := 6; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		lines, err := captureLines(filepath.Join(dir, day.Format(layout)+".md"))
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(stdout, "## %s\n", day.Format(dailyFormat))
		for _, line := range lines {
			fmt.Fprintln(stdout, line)
		}
	}
	return nil
}
