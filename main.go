package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// dailyFormat is the default Go time layout for the daily note filename.
const dailyFormat = "2006-01-02"

const helpText = `Usage: capture [flags] text...
       echo "line" | capture

Appends each capture as an entry to today's daily note in an Obsidian vault.
Success is silent (exit 0); errors go to stderr (exit 1).

Input rules:
  Arguments: all remaining words are joined with single spaces into one entry.
  Stdin: when piped, each non-empty line becomes its own entry (blank lines
  are skipped, lines are trimmed).
  Arguments and piped stdin together is an error; empty input is an error.
  Use -- before text that starts with a dash, e.g. capture -- -todo list.

Flags:
  -t, --todo
        record a task instead of a note (renders "- [ ] text"); without it the
        entry is a note (renders "- HH:MM | text").
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

// version reports the module version baked in by `go install pkg@vX.Y.Z`,
// so git tags drive releases with no constant to bump. Local builds have
// no module version and report "devel".
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
}

func run(args []string, getenv func(string) string, now time.Time, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var task bool
	flags.BoolVar(&task, "t", false, "record a task instead of a note")
	flags.BoolVar(&task, "todo", false, "record a task instead of a note")
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

	text := strings.Join(flags.Args(), " ")
	if piped && text != "" {
		return errors.New("arguments and stdin at once are ambiguous")
	}
	if !piped && text == "" {
		return errors.New("empty capture")
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
	// Task entries render "- [ ] text" with no timestamp; note entries
	// render "- HH:MM | text". Piped stdin yields one entry per non-empty
	// trimmed line; the task flag applies to stdin-derived entries too.
	entry := func(s string) string {
		if task {
			return "- [ ] " + s + "\n"
		}
		return "- " + now.Format("15:04") + " | " + s + "\n"
	}
	var buf strings.Builder
	if piped {
		for _, l := range strings.Split(string(raw), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				buf.WriteString(entry(l))
			}
		}
	} else {
		buf.WriteString(entry(text))
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
