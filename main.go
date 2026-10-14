package main

import (
	"errors"
	"flag"
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
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var task bool
	flags.BoolVar(&task, "t", false, "record a task instead of a note")
	flags.BoolVar(&task, "todo", false, "record a task instead of a note")
	if err := flags.Parse(args); err != nil {
		return err
	}

	// Stdin counts as piped when it holds non-whitespace content: tests (and
	// the terminal case) use an empty reader for "no stdin", so no isatty
	// check is needed. Content is read once, whole, before any validation.
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	piped := strings.TrimSpace(string(raw)) != ""

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

	path := filepath.Join(vault, now.Format(dailyFormat)+".md")
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
