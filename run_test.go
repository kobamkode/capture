package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixed clock in an explicit zone so assertions never depend on the machine's local zone.
var testClock = time.Date(2026, 10, 14, 9, 15, 0, 0, time.FixedZone("UTC+7", 7*3600))

func TestRun(t *testing.T) {
	cases := []struct {
		name         string
		vaultMode    string // "unset", "missing", "file", "dir"
		dailyDir     string // value for CAPTURE_DAILY_DIR; "" = unset
		makeDailyDir bool   // create vault/dailyDir before running
		dailyFormat  string // value for CAPTURE_DAILY_FORMAT; "" = unset
		args         []string
		stdin        string
		now          time.Time
		preExisting  *string
		wantErr      string
		wantFile     string
		want         string
	}{
		{
			name:      "CAPTURE_VAULT unset fails naming the variable",
			vaultMode: "unset",
			args:      []string{"hello"},
			wantErr:   "CAPTURE_VAULT",
		},
		{
			name:      "CAPTURE_VAULT missing path fails naming the variable",
			vaultMode: "missing",
			args:      []string{"hello"},
			wantErr:   "CAPTURE_VAULT",
		},
		{
			name:      "CAPTURE_VAULT pointing at a file fails",
			vaultMode: "file",
			args:      []string{"hello"},
			wantErr:   "CAPTURE_VAULT",
		},
		{
			name:      "empty capture fails",
			vaultMode: "dir",
			args:      nil,
			wantErr:   "empty capture",
		},
		{
			name:      "creates daily note at vault root",
			vaultMode: "dir",
			args:      []string{"hello"},
			wantFile:  "2026-10-14.md",
			want:      "- 09:15 | hello\n",
		},
		{
			name:      "joins all args with single spaces into one entry",
			vaultMode: "dir",
			args:      []string{"i", "think", "i", "need", "to", "learn", "math"},
			wantFile:  "2026-10-14.md",
			want:      "- 09:15 | i think i need to learn math\n",
		},
		{
			name:      "appends to existing note",
			vaultMode: "dir",
			args:      []string{"later", "thought"},
			preExisting: func() *string {
				s := "- 08:00 | earlier\n"
				return &s
			}(),
			wantFile: "2026-10-14.md",
			want:     "- 08:00 | earlier\n- 09:15 | later thought\n",
		},
		{
			name:      "single stdin line becomes one note entry",
			vaultMode: "dir",
			stdin:     "  hello world  \n",
			wantFile:  "2026-10-14.md",
			want:      "- 09:15 | hello world\n",
		},
		{
			name:      "multiple stdin lines become one entry each",
			vaultMode: "dir",
			stdin:     "first line\nsecond line\nthird line\n",
			wantFile:  "2026-10-14.md",
			want:      "- 09:15 | first line\n- 09:15 | second line\n- 09:15 | third line\n",
		},
		{
			name:      "blank stdin lines skipped and lines trimmed",
			vaultMode: "dir",
			stdin:     "  one  \n\n   \n\t two\t\n",
			wantFile:  "2026-10-14.md",
			want:      "- 09:15 | one\n- 09:15 | two\n",
		},
		{
			name:      "args and stdin together is a hard error",
			vaultMode: "dir",
			args:      []string{"hello"},
			stdin:     "piped\n",
			wantErr:   "arguments and stdin",
		},
		{
			name:      "no args and whitespace-only stdin is empty capture",
			vaultMode: "dir",
			stdin:     "  \n \t \n",
			wantErr:   "empty capture",
		},
		{
			name:      "-t renders a task checkbox line with no timestamp",
			vaultMode: "dir",
			args:      []string{"-t", "go to dentist"},
			wantFile:  "2026-10-14.md",
			want:      "- [ ] go to dentist\n",
		},
		{
			name:      "--todo renders the same task line as -t",
			vaultMode: "dir",
			args:      []string{"--todo", "go to dentist"},
			wantFile:  "2026-10-14.md",
			want:      "- [ ] go to dentist\n",
		},
		{
			name:      "-- separator captures dash-leading text as a note",
			vaultMode: "dir",
			args:      []string{"--", "-todo", "list"},
			wantFile:  "2026-10-14.md",
			want:      "- 09:15 | -todo list\n",
		},
		{
			name:      "dash-word after first positional stays text",
			vaultMode: "dir",
			args:      []string{"buy", "milk", "-todo"},
			wantFile:  "2026-10-14.md",
			want:      "- 09:15 | buy milk -todo\n",
		},
		// Flags come before the text: a trailing -l is captured verbatim,
		// so the run succeeds silently with no listing.
		{
			name:      "trailing -l after first positional stays text",
			vaultMode: "dir",
			args:      []string{"hello", "-l"},
			wantFile:  "2026-10-14.md",
			want:      "- 09:15 | hello -l\n",
		},
		{
			name:      "-t with no text fails empty capture",
			vaultMode: "dir",
			args:      []string{"-t"},
			wantErr:   "empty capture",
		},
		{
			name:      "-t applies to stdin lines rendering tasks",
			vaultMode: "dir",
			args:      []string{"-t"},
			stdin:     "  first task  \n\n   \nsecond task\n",
			wantFile:  "2026-10-14.md",
			want:      "- [ ] first task\n- [ ] second task\n",
		},
		{
			name:      "-t with args and stdin together is a hard error",
			vaultMode: "dir",
			args:      []string{"-t", "hello"},
			stdin:     "piped\n",
			wantErr:   "arguments and stdin",
		},
		{
			name:      "repairs missing trailing newline before append",
			vaultMode: "dir",
			args:      []string{"new"},
			preExisting: func() *string {
				s := "- 08:00 | earlier"
				return &s
			}(),
			wantFile: "2026-10-14.md",
			want:     "- 08:00 | earlier\n- 09:15 | new\n",
		},
		{
			name:      "CAPTURE_DAILY_DIR unset writes at vault root",
			vaultMode: "dir",
			args:      []string{"hello"},
			wantFile:  "2026-10-14.md",
			want:      "- 09:15 | hello\n",
		},
		{
			name:         "CAPTURE_DAILY_DIR set and existing writes inside the folder",
			vaultMode:    "dir",
			dailyDir:     "Journal",
			makeDailyDir: true,
			args:         []string{"hello"},
			wantFile:     filepath.Join("Journal", "2026-10-14.md"),
			want:         "- 09:15 | hello\n",
		},
		{
			name:      "CAPTURE_DAILY_DIR set and missing fails naming the variable",
			vaultMode: "dir",
			dailyDir:  "Missing",
			args:      []string{"hello"},
			wantErr:   "CAPTURE_DAILY_DIR",
		},
		{
			name:        "CAPTURE_DAILY_FORMAT unset uses the default name",
			vaultMode:   "dir",
			dailyFormat: "",
			args:        []string{"hello"},
			wantFile:    "2026-10-14.md",
			want:        "- 09:15 | hello\n",
		},
		{
			name:        "CAPTURE_DAILY_FORMAT set renames the file",
			vaultMode:   "dir",
			dailyFormat: "02-01-2006",
			args:        []string{"hello"},
			wantFile:    "14-10-2026.md",
			want:        "- 09:15 | hello\n",
		},
		{
			name:        "invalid CAPTURE_DAILY_FORMAT fails naming the variable",
			vaultMode:   "dir",
			dailyFormat: "%Y-%m-%d",
			args:        []string{"hello"},
			wantErr:     "CAPTURE_DAILY_FORMAT",
		},
		{
			name:      "-l rejects text and leaves the existing Daily note untouched",
			vaultMode: "dir",
			args:      []string{"-l", "some", "text"},
			preExisting: func() *string {
				s := "- 08:00 | earlier\n"
				return &s
			}(),
			wantErr: "no capture input",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := t.TempDir()
			switch tc.vaultMode {
			case "missing":
				vault = filepath.Join(t.TempDir(), "no-such-vault")
			case "file":
				vault = filepath.Join(t.TempDir(), "not-a-dir")
				if err := os.WriteFile(vault, []byte("x"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			var vaultValue string
			if tc.vaultMode != "unset" {
				vaultValue = vault
			}
			if tc.makeDailyDir {
				if err := os.Mkdir(filepath.Join(vault, tc.dailyDir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.preExisting != nil {
				if err := os.WriteFile(filepath.Join(vault, "2026-10-14.md"), []byte(*tc.preExisting), 0644); err != nil {
					t.Fatal(err)
				}
			}

			now := tc.now
			if now.IsZero() {
				now = testClock
			}
			var stdout, stderr bytes.Buffer
			getenv := func(key string) string {
				switch key {
				case "CAPTURE_VAULT":
					return vaultValue
				case "CAPTURE_DAILY_DIR":
					return tc.dailyDir
				case "CAPTURE_DAILY_FORMAT":
					return tc.dailyFormat
				}
				return ""
			}

			err := run(tc.args, getenv, now, strings.NewReader(tc.stdin), &stdout, &stderr)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("run returned nil, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				if tc.dailyDir != "" && !tc.makeDailyDir {
					if _, statErr := os.Stat(filepath.Join(vault, tc.dailyDir)); !os.IsNotExist(statErr) {
						t.Errorf("folder %s must not be created on failure", tc.dailyDir)
					}
				}
				// No failing invocation may create or modify the Daily note.
				if tc.preExisting != nil {
					got, readErr := os.ReadFile(filepath.Join(vault, "2026-10-14.md"))
					if readErr != nil || string(got) != *tc.preExisting {
						t.Errorf("Daily note must be unchanged on failure, got %q (err %v)", got, readErr)
					}
				} else if _, statErr := os.Stat(filepath.Join(vault, "2026-10-14.md")); statErr == nil {
					t.Errorf("no failing invocation may create the Daily note")
				}
				return
			}
			if err != nil {
				t.Fatalf("run returned unexpected error: %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("success must be silent, got stdout=%q stderr=%q", stdout.String(), stderr.String())
			}

			path := filepath.Join(vault, tc.wantFile)
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			if string(got) != tc.want {
				t.Errorf("content of %s = %q, want %q", tc.wantFile, got, tc.want)
			}
		})
	}
}

// statReader implements io.Reader plus the Stat method run() type-asserts on,
// so tests can fake a terminal without a real TTY. With no data its Read
// always fails: a char-device stdin must never be read.
type statReader struct {
	mode fs.FileMode
	r    *strings.Reader
	read bool
}

func newStatReader(mode fs.FileMode, data string) *statReader {
	s := &statReader{mode: mode}
	if data != "" {
		s.r = strings.NewReader(data)
	}
	return s
}

func (r *statReader) Read(p []byte) (int, error) {
	r.read = true
	if r.r == nil {
		return 0, errors.New("statReader: Read called but must not be")
	}
	return r.r.Read(p)
}

func (r *statReader) Stat() (os.FileInfo, error) { return statInfo{r.mode}, nil }

// statInfo is an os.FileInfo whose Mode is whatever the test needs.
type statInfo struct{ mode fs.FileMode }

func (i statInfo) Name() string       { return "fake" }
func (i statInfo) Size() int64        { return 0 }
func (i statInfo) Mode() fs.FileMode  { return i.mode }
func (i statInfo) ModTime() time.Time { return time.Time{} }
func (i statInfo) IsDir() bool        { return false }
func (i statInfo) Sys() any           { return nil }

// --version must succeed with CAPTURE_VAULT unset, print to stdout only,
// and write no daily note.
func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--version"}, func(string) string { return "" }, testClock,
		strings.NewReader("piped text"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("run(--version) returned %v, want nil", err)
	}
	if !strings.HasPrefix(stdout.String(), "capture ") {
		t.Errorf("--version stdout = %q, want prefix %q", stdout.String(), "capture ")
	}
	if stderr.Len() != 0 {
		t.Errorf("--version stderr = %q, want empty", stderr.String())
	}
}

func TestRunTreatsCharDeviceStdinAsAbsent(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	stdin := newStatReader(os.ModeCharDevice, "")

	var stdout, stderr bytes.Buffer
	err := run([]string{"quick", "thought"}, getenv, testClock, stdin, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned %v, want nil for char-device stdin", err)
	}
	if stdin.read {
		t.Error("stdin was read on a char device; a terminal would block until EOF")
	}
	got, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "- 09:15 | quick thought\n"; string(got) != want {
		t.Errorf("2026-10-14.md = %q, want %q", got, want)
	}
}

func TestRunReadsNonCharStatableStdin(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	// Stat says "not a terminal" (regular file / pipe): read path as today.
	stdin := newStatReader(0, "  first  \n\nsecond\n")

	var stdout, stderr bytes.Buffer
	err := run(nil, getenv, testClock, stdin, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned %v, want nil", err)
	}
	if !stdin.read {
		t.Error("stdin not read although Stat reported a non-char device")
	}
	got, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "- 09:15 | first\n- 09:15 | second\n"; string(got) != want {
		t.Errorf("2026-10-14.md = %q, want %q", got, want)
	}
}

func TestRunHelpDocumentsFlagsAndEnvVars(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--help"}, func(string) string { return "" }, testClock,
		strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("run(--help) returned %v, want nil", err)
	}
	help := stdout.String()
	if help == "" {
		t.Fatal("--help wrote nothing to stdout")
	}
	for _, want := range []string{
		"-t", "--todo", "-l, --list", "-version",
		"--", "stdin",
		"CAPTURE_VAULT", "CAPTURE_DAILY_DIR", "CAPTURE_DAILY_FORMAT",
		"2006-01-02",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("--help output missing %q:\n%s", want, help)
		}
	}
}

func TestRunMixesNotesAndTasksInOneFile(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}

	steps := [][]string{
		{"first", "thought"},
		{"-t", "go to dentist"},
		{"--todo", "buy milk"},
	}
	for _, args := range steps {
		var stdout, stderr bytes.Buffer
		if err := run(args, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
	}

	got, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "- 09:15 | first thought\n- [ ] go to dentist\n- [ ] buy milk\n"
	if string(got) != want {
		t.Errorf("2026-10-14.md = %q, want %q", got, want)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(got), "\n"), "\n") {
		if strings.HasPrefix(line, "- [ ] ") && strings.Contains(line, "|") {
			t.Errorf("task line %q must never carry a timestamp or pipe", line)
		}
	}
}

func TestRunAccumulatesAndRollsOver(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	zone := time.FixedZone("UTC+7", 7*3600)
	dayOne := time.Date(2026, 10, 14, 9, 15, 0, 0, zone)
	dayOneLate := time.Date(2026, 10, 14, 23, 59, 0, 0, zone)
	dayTwo := time.Date(2026, 10, 15, 0, 1, 0, 0, zone)

	steps := []struct {
		now  time.Time
		args []string
	}{
		{dayOne, []string{"first"}},
		{dayOneLate, []string{"second"}},
		{dayTwo, []string{"third"}},
	}
	for _, step := range steps {
		var stdout, stderr bytes.Buffer
		if err := run(step.args, getenv, step.now, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("run(%v) at %v: %v", step.args, step.now, err)
		}
	}

	dayOneContent, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantDayOne := "- 09:15 | first\n- 23:59 | second\n"
	if string(dayOneContent) != wantDayOne {
		t.Errorf("2026-10-14.md = %q, want %q", dayOneContent, wantDayOne)
	}

	dayTwoContent, err := os.ReadFile(filepath.Join(vault, "2026-10-15.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantDayTwo := "- 00:01 | third\n"
	if string(dayTwoContent) != wantDayTwo {
		t.Errorf("2026-10-15.md = %q, want %q", dayTwoContent, wantDayTwo)
	}
}

// -l prints today's Notes and Tasks verbatim in file order, filtering template
// headings, prose and blank lines, and never touches the Daily note.
// --list is the same flag.
func TestRunListPrintsCaptureLinesOnly(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	path := filepath.Join(vault, "2026-10-14.md")
	content := "# Daily\n\nsome prose\n\n- 08:00 | earlier note\n- [ ] open the door\n" +
		"- just a bullet\n- [x] done thing\n- [X] also done\n- 09:15 | later thought\n\n## Tasks\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"-l"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(-l): %v", err)
	}
	want := "- 08:00 | earlier note\n- [ ] open the door\n- [x] done thing\n" +
		"- [X] also done\n- 09:15 | later thought\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Errorf("listing touched the Daily note: mtime/size changed")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("listing modified the Daily note: %q", got)
	}

	stdout.Reset()
	if err := run([]string{"--list"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(--list): %v", err)
	}
	if stdout.String() != want {
		t.Errorf("--list stdout = %q, want %q", stdout.String(), want)
	}
}

// A missing Daily note, or one holding no capture lines, is a normal empty
// result: no output, exit 0, nothing created or changed anywhere.
func TestRunListEmptyResults(t *testing.T) {
	cases := []struct {
		name string
		note string // "" = no Daily note exists
	}{
		{"missing daily note", ""},
		{"daily note without capture lines", "# Daily\n\nsome prose\n\n- bring snacks\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := t.TempDir()
			getenv := func(key string) string {
				if key == "CAPTURE_VAULT" {
					return vault
				}
				return ""
			}
			path := filepath.Join(vault, "2026-10-14.md")
			if tc.note != "" {
				if err := os.WriteFile(path, []byte(tc.note), 0644); err != nil {
					t.Fatal(err)
				}
			}

			var stdout, stderr bytes.Buffer
			if err := run([]string{"-l"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Fatalf("run(-l): %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("want empty output, got stdout=%q stderr=%q", stdout.String(), stderr.String())
			}

			entries, err := os.ReadDir(vault)
			if err != nil {
				t.Fatal(err)
			}
			wantEntries := 1
			if tc.note == "" {
				wantEntries = 0
			}
			if len(entries) != wantEntries {
				t.Errorf("vault holds %d entries (%v), want %d: nothing may be created", len(entries), entries, wantEntries)
			}
			if tc.note != "" {
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tc.note {
					t.Errorf("Daily note changed: %q", got)
				}
			}
		})
	}
}

// The listed file honours CAPTURE_DAILY_DIR and CAPTURE_DAILY_FORMAT: it is
// the file a capture appends to, and a capture made just before listing on the
// same clock shows up in the output.
func TestRunListHonoursCustomDailyLayout(t *testing.T) {
	vault := t.TempDir()
	if err := os.Mkdir(filepath.Join(vault, "Journal"), 0755); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		switch key {
		case "CAPTURE_VAULT":
			return vault
		case "CAPTURE_DAILY_DIR":
			return "Journal"
		case "CAPTURE_DAILY_FORMAT":
			return "02-01-2006"
		}
		return ""
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"hello", "world"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("capture: %v", err)
	}

	stdout.Reset()
	if err := run([]string{"-l"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(-l): %v", err)
	}
	want := "- 09:15 | hello world\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}

	entries, err := os.ReadDir(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "Journal" {
		t.Errorf("vault entries = %v, want only the Journal folder", entries)
	}
	got, err := os.ReadFile(filepath.Join(vault, "Journal", "14-10-2026.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("Journal/14-10-2026.md = %q, want %q", got, want)
	}
}

// List mode accepts no capture input: under -l and --list alike, positional
// text, piped stdin and the task flag each fail through run's error return
// (stderr and exit 1 via main) and never create the Daily note.
func TestRunListRejectsCaptureInput(t *testing.T) {
	cases := []struct {
		name  string
		args  []string // args after the list flag
		stdin string
	}{
		{"positional text", []string{"some", "text"}, ""},
		{"piped stdin", nil, "ideas\n"},
		{"task flag -t", []string{"-t"}, ""},
		{"task flag --todo", []string{"--todo"}, ""},
		{"args and stdin together", []string{"some", "text"}, "ideas\n"},
	}
	for _, listFlag := range []string{"-l", "--list"} {
		for _, tc := range cases {
			t.Run(listFlag+" with "+tc.name, func(t *testing.T) {
				vault := t.TempDir()
				getenv := func(key string) string {
					if key == "CAPTURE_VAULT" {
						return vault
					}
					return ""
				}
				args := append([]string{listFlag}, tc.args...)

				var stdout, stderr bytes.Buffer
				err := run(args, getenv, testClock, strings.NewReader(tc.stdin), &stdout, &stderr)
				if err == nil {
					t.Fatalf("run(%v) returned nil, want error", args)
				}
				if !strings.Contains(err.Error(), "no capture input") {
					t.Errorf("error = %q, want it to name the rejected capture input", err.Error())
				}
				if stdout.Len() != 0 {
					t.Errorf("stdout = %q, want empty", stdout.String())
				}
				if _, statErr := os.Stat(filepath.Join(vault, "2026-10-14.md")); !os.IsNotExist(statErr) {
					t.Error("a failing list invocation must not create the Daily note")
				}
			})
		}
	}
}

// Vault and daily-note configuration errors surface under -l with
// byte-identical messages to a capture, and neither leg creates the Daily note.
func TestRunListConfigErrorsMatchCapture(t *testing.T) {
	cases := []struct {
		name        string
		makeVault   func(t *testing.T) string // CAPTURE_VAULT value
		dailyDir    string
		dailyFormat string
	}{
		{"unset CAPTURE_VAULT", func(*testing.T) string { return "" }, "", ""},
		{"CAPTURE_VAULT not a directory", func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "not-a-dir")
			if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
			return p
		}, "", ""},
		{"missing CAPTURE_DAILY_DIR", func(t *testing.T) string { return t.TempDir() }, "Missing", ""},
		{"invalid CAPTURE_DAILY_FORMAT", func(t *testing.T) string { return t.TempDir() }, "", "%Y-%m-%d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := tc.makeVault(t)
			getenv := func(key string) string {
				switch key {
				case "CAPTURE_VAULT":
					return vault
				case "CAPTURE_DAILY_DIR":
					return tc.dailyDir
				case "CAPTURE_DAILY_FORMAT":
					return tc.dailyFormat
				}
				return ""
			}

			var captureOut, captureErr, listOut, listErr bytes.Buffer
			errCapture := run([]string{"hello"}, getenv, testClock, strings.NewReader(""), &captureOut, &captureErr)
			errList := run([]string{"-l"}, getenv, testClock, strings.NewReader(""), &listOut, &listErr)

			if errCapture == nil || errList == nil {
				t.Fatalf("want errors from both legs, got capture=%v list=%v", errCapture, errList)
			}
			if errList.Error() != errCapture.Error() {
				t.Errorf("list error %q != capture error %q", errList.Error(), errCapture.Error())
			}
			if info, statErr := os.Stat(vault); statErr == nil && info.IsDir() {
				entries, readErr := os.ReadDir(vault)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if len(entries) != 0 {
					t.Errorf("failing invocations created %v in the vault", entries)
				}
			}
		})
	}
}
