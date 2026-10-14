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

	text := strings.Join(flags.Args(), " ")
	if text == "" {
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
	line := "- " + now.Format("15:04") + " | " + text + "\n"
	if task {
		line = "- [ ] " + text + "\n"
	}

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
