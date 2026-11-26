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
       capture find <id>
       capture move
       echo "line" | capture

Appends each capture as an entry to today's daily note in an Obsidian vault.
A successful capture prints nothing (exit 0); -l writes the listing to stdout;
errors go to stderr (exit 1).

Commands:
  open
        open today's daily note in Obsidian, creating it when missing.
        Takes no flags, text, or stdin. To capture the word itself, use the
        -- separator: capture -- open.
  find <id>
        print every capture whose id column equals <id>, as path:line:text.
        Reads the daily notes in $CAPTURE_VAULT (or CAPTURE_DAILY_DIR) across
        all days, oldest filename first; files whose name is not a daily note
        under CAPTURE_DAILY_FORMAT are ignored. No match is an empty result:
        no output, exit 0. Takes no flags, no stdin, and exactly one id. To
        capture the word itself, use -- : capture -- find <text>.
  move
        move the unfinished tasks of the previous daily note into today's:
        append them verbatim, delete them from the source, and print what
        moved. The previous daily note is the newest daily note dated before
        today, however old; when it is missing or holds nothing unfinished
        nothing moves: no output, exit 0, no file created. Moved tasks keep
        their id and capture time, so capture find still calls them back.
        Checked tasks, notes, prose and nested checkboxes stay where they are.
        Takes no flags, text, or stdin. To capture the word itself, use -- :
        capture -- move.

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
	// `capture open`, `capture find` and `capture move` are commands, not
	// captures of the words themselves. Each must be the first argument, so
	// `capture -t open` and `capture -- move` still capture the text.
	openMode := len(args) > 0 && args[0] == "open"
	findMode := len(args) > 0 && args[0] == "find"
	moveMode := len(args) > 0 && args[0] == "move"
	if openMode || findMode || moveMode {
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
	} else if findMode {
		// Find takes exactly one id and nothing else: a mistyped call must fail
		// loudly instead of being treated as the text "find".
		if task || list || week || len(flags.Args()) != 1 {
			return errors.New("find takes exactly one capture id")
		}
	} else if moveMode {
		// Move takes no configuration beyond the environment: reject flags and
		// text before reading stdin or touching the vault.
		if task || list || week || len(flags.Args()) > 0 {
			return errors.New("move takes no flags, text, or stdin")
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
	} else if findMode {
		if piped {
			return errors.New("find takes no stdin")
		}
	} else if moveMode {
		if piped {
			return errors.New("move takes no stdin")
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

	notes := dailyNotes{dir: dir, layout: layout, now: now}

	// Find is a lookup across the stored days rather than today's note: it
	// reads the directory and never creates or writes anything.
	if findMode {
		return findCaptures(notes, flags.Args()[0], stdout)
	}

	path := notes.notePath(now)
	if moveMode {
		return moveTasks(notes, stdout)
	}
	if openMode {
		if err := ensureNote(path); err != nil {
			return err
		}
		return open(obsidianURL(path))
	}
	if list {
		if week {
			return listWeek(notes, stdout)
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
	return appendToNote(path, buf.String())
}

// appendToNote adds line to the end of the Daily note, creating the note when
// it is missing. A note that already holds text without a trailing newline
// gets one first, so the new entry never lands on the last line of the old one.
func appendToNote(path, line string) error {
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

// rewriteNote replaces the Daily note at path with content through a temporary
// file in the same directory renamed over it, so an interrupted write cannot
// leave the note truncated. The note keeps existing even when content is empty,
// and keeps its own permissions: a 0600 note must not come back world readable.
func rewriteNote(path, content string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".capture-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once the rename below succeeds
	if _, err := io.WriteString(f, content); err != nil {
		f.Close()
		return err
	}
	// Sync before the rename, or a crash can publish a zero-length note.
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// A missing note (stat error) falls back to the create default.
	mode := fs.FileMode(0644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

// findID is the shape of a capture id: seven lowercase hex characters, the
// same shape randomID renders. Anything else is rejected before the vault is
// touched, so a truncated or pasted-wrong id can never match another column.
var findID = regexp.MustCompile(`^[0-9a-f]{7}$`)

// idColumn matches a capture-shaped line up to and including its id column and
// captures the id. It accepts both capture types and the checked task boxes a
// vault edit produces, and is anchored at the line start, so an id quoted
// inside a capture's text is not mistaken for a column.
var idColumn = regexp.MustCompile(
	`^(?:` + regexp.QuoteMeta(notePrefix) + `\d{2}:\d{2}` +
		`|` + regexp.QuoteMeta(taskPrefix) + `\| \d{2}:\d{2}` +
		`|- \[[xX]\] \| \d{2}:\d{2})` +
		regexp.QuoteMeta(colSep) + `([0-9a-f]{7})` + regexp.QuoteMeta(colSep))

// dailyNotes is where the Daily notes live, how their filenames are laid out
// and which day counts as today: the three facts every command that walks the
// vault needs together.
type dailyNotes struct {
	dir    string
	layout string
	now    time.Time
}

// notePath names the Daily note for one day under the configured layout.
func (d dailyNotes) notePath(day time.Time) string {
	return filepath.Join(d.dir, day.Format(d.layout)+".md")
}

// names lists the Daily notes in the directory, in filename order, skipping
// anything whose name is not a day under the layout. Order is the directory's,
// not the calendar's: a caller that needs the days themselves parses each name.
func (d dailyNotes) names() ([]string, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		if _, err := time.Parse(d.layout, strings.TrimSuffix(name, ".md")); err != nil {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// findCaptures prints every capture whose id column equals id, as
// "path:line:text", oldest daily note first. Only files named by the daily
// layout are read, so unrelated Markdown in the vault cannot supply a hit and
// a misconfigured vault is not swept. A missing id is an empty result: no
// output, no error, nothing created.
func findCaptures(notes dailyNotes, id string, stdout io.Writer) error {
	if !findID.MatchString(id) {
		return fmt.Errorf("not a capture id (seven lowercase hex characters): %s", id)
	}
	names, err := notes.names()
	if err != nil {
		return err
	}
	for _, name := range names {
		path := filepath.Join(notes.dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
			if m := idColumn.FindStringSubmatch(line); m != nil && m[1] == id {
				fmt.Fprintf(stdout, "%s:%d:%s\n", path, i+1, line)
			}
		}
	}
	return nil
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
func listWeek(notes dailyNotes, stdout io.Writer) error {
	for i := 6; i >= 0; i-- {
		day := notes.now.AddDate(0, 0, -i)
		lines, err := captureLines(notes.notePath(day))
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

// dateOf drops the clock so two days compare as calendar dates, whatever time
// of day the layout parses or the clock reports.
func dateOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// previousDailyNote returns the newest Daily note dated strictly before today,
// however old. Filenames are parsed under layout instead of sorted as strings,
// so a layout that does not start with the date still works and an old note
// edited today is not promoted by its modification time. Ties on one date go
// to the greater filename.
func previousDailyNote(notes dailyNotes) (string, bool, error) {
	names, err := notes.names()
	if err != nil {
		return "", false, err
	}
	today := dateOf(notes.now)
	best := ""
	var bestDay time.Time
	for _, name := range names {
		parsed, err := time.Parse(notes.layout, strings.TrimSuffix(name, ".md"))
		if err != nil {
			continue
		}
		day := dateOf(parsed)
		if !day.Before(today) {
			continue
		}
		if best == "" || day.After(bestDay) || day.Equal(bestDay) && name > best {
			best, bestDay = name, day
		}
	}
	if best == "" {
		return "", false, nil
	}
	return filepath.Join(notes.dir, best), true, nil
}

// splitLines keeps each line's trailing newline, so joining the result
// reproduces the input byte for byte; the last line may have none.
func splitLines(s string) []string {
	var lines []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return append(lines, s)
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

// moveTasks drains the Previous daily note's unfinished Tasks into today's
// Daily note: each movable line is appended verbatim and then deleted from the
// source, leaving every other byte of it alone. Today's note is appended to
// first and the source rewritten second, so an interruption duplicates a Task
// at worst instead of losing one. Nothing movable means no output, no error
// and no file created for today.
func moveTasks(notes dailyNotes, stdout io.Writer) error {
	source, ok, err := previousDailyNote(notes)
	if err != nil || !ok {
		return err
	}
	b, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	var kept, moved strings.Builder
	for _, line := range splitLines(string(b)) {
		text := strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(text, taskPrefix) {
			moved.WriteString(text + "\n")
			continue
		}
		kept.WriteString(line)
	}
	if moved.Len() == 0 {
		return nil
	}
	if err := appendToNote(notes.notePath(notes.now), moved.String()); err != nil {
		return err
	}
	if err := rewriteNote(source, kept.String()); err != nil {
		return err
	}
	_, err = io.WriteString(stdout, moved.String())
	return err
}
