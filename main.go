package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dailyFormat is the default Go time layout for the daily note filename.
const dailyFormat = "2006-01-02"

func main() {
	if err := run(os.Args[1:], os.Getenv, time.Now(), os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, getenv func(string) string, now time.Time, stdin io.Reader, stdout, stderr io.Writer) error {
	// Stdin counts as piped when it holds non-whitespace content: tests (and
	// the terminal case) use an empty reader for "no stdin", so no isatty
	// check is needed. Content is read once, whole, before any validation.
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	piped := strings.TrimSpace(string(raw)) != ""

	text := strings.Join(args, " ")
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
	// Note entries only in this ticket: every non-empty trimmed stdin line,
	// or the joined args, renders as one "- HH:MM | text" line.
	var buf strings.Builder
	if piped {
		for _, l := range strings.Split(string(raw), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				buf.WriteString("- " + now.Format("15:04") + " | " + l + "\n")
			}
		}
	} else {
		buf.WriteString("- " + now.Format("15:04") + " | " + text + "\n")
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
