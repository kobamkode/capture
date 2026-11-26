package main

import (
	"bytes"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Fixed clock in an explicit zone so assertions never depend on the machine's local zone.
var testClock = time.Date(2026, 10, 14, 9, 15, 0, 0, time.FixedZone("UTC+7", 7*3600))

// The id column of a rendered line, which follows the timestamp of both
// capture types. Tests strip the column so an expectation can state the prose
// a line carries; TestRunSealsEachCaptureWithAUniqueID pins the ids.
var (
	noteIDColumn = regexp.MustCompile(`^(- \d{2}:\d{2} \| )[0-9a-f]{7} \| `)
	taskIDColumn = regexp.MustCompile(`^(- \[ \] \| \d{2}:\d{2} \| )[0-9a-f]{7} \| `)
	captureID    = regexp.MustCompile(` \| ([0-9a-f]{7}) \| `)
	timeOfDay    = regexp.MustCompile(`\d{2}:\d{2}`)
)

// withoutIDs drops the id column from every line of s, leaving the note form
// "- HH:MM | text" and the task form "- [ ] | HH:MM | text".
func withoutIDs(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = taskIDColumn.ReplaceAllString(line, "$1")
		lines[i] = noteIDColumn.ReplaceAllString(line, "$1")
	}
	return strings.Join(lines, "\n")
}

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
			name:      "-t renders a task checkbox line with the capture time",
			vaultMode: "dir",
			args:      []string{"-t", "go to dentist"},
			wantFile:  "2026-10-14.md",
			want:      "- [ ] | 09:15 | go to dentist\n",
		},
		{
			name:      "--todo renders the same task line as -t",
			vaultMode: "dir",
			args:      []string{"--todo", "go to dentist"},
			wantFile:  "2026-10-14.md",
			want:      "- [ ] | 09:15 | go to dentist\n",
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
			want:      "- [ ] | 09:15 | first task\n- [ ] | 09:15 | second task\n",
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
			if withoutIDs(string(got)) != tc.want {
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
	if want := "- 09:15 | quick thought\n"; withoutIDs(string(got)) != want {
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
	if want := "- 09:15 | first\n- 09:15 | second\n"; withoutIDs(string(got)) != want {
		t.Errorf("2026-10-14.md = %q, want %q", got, want)
	}
}

// Every rendered line carries its own capture id column: seven hex characters
// between pipes, the shape of a git short hash, sitting after the timestamp of
// a Note or a Task. Ids are random, so two entries never
// share one; -l prints them back verbatim, which is what lets a later reader
// grep the row back.
func TestRunSealsEachCaptureWithAUniqueID(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}

	steps := [][]string{
		{"a", "note"},
		{"-t", "a", "task"},
	}
	for _, args := range steps {
		var stdout, stderr bytes.Buffer
		if err := run(args, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
	}
	var stdout, stderr bytes.Buffer
	if err := run(nil, getenv, testClock, strings.NewReader("piped one\npiped two\n"), &stdout, &stderr); err != nil {
		t.Fatalf("run from stdin: %v", err)
	}

	note, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(note), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("wrote %d lines, want 4: %q", len(lines), note)
	}
	seen := make(map[string]string)
	for _, line := range lines {
		// The shape regexps verify the column sits where it belongs; captureID
		// pulls the id out of it.
		if !noteIDColumn.MatchString(line) && !taskIDColumn.MatchString(line) {
			t.Errorf("line %q has no capture id column", line)
			continue
		}
		id := captureID.FindStringSubmatch(line)[1]
		if prev, dup := seen[id]; dup {
			t.Errorf("id %q used by both %q and %q", id, prev, line)
		}
		seen[id] = line
	}

	// The listing carries the same ids verbatim, so a row can be called back.
	stdout.Reset()
	if err := run([]string{"-l"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(-l): %v", err)
	}
	if stdout.String() != string(note) {
		t.Errorf("-l output = %q, want the Daily note verbatim %q", stdout.String(), note)
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
		"-t", "--todo", "-l, --list", "-w, --week", "-version",
		"open",
		"--", "stdin",
		"CAPTURE_VAULT", "CAPTURE_DAILY_DIR", "CAPTURE_DAILY_FORMAT",
		"2006-01-02", "capture id",
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
	want := "- 09:15 | first thought\n- [ ] | 09:15 | go to dentist\n- [ ] | 09:15 | buy milk\n"
	if withoutIDs(string(got)) != want {
		t.Errorf("2026-10-14.md = %q, want %q", got, want)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(got), "\n"), "\n") {
		if strings.HasPrefix(line, "- [ ] ") && !timeOfDay.MatchString(line) {
			t.Errorf("task line %q must carry a timestamp", line)
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
	if withoutIDs(string(dayOneContent)) != wantDayOne {
		t.Errorf("2026-10-14.md = %q, want %q", dayOneContent, wantDayOne)
	}

	dayTwoContent, err := os.ReadFile(filepath.Join(vault, "2026-10-15.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantDayTwo := "- 00:01 | third\n"
	if withoutIDs(string(dayTwoContent)) != wantDayTwo {
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
	if withoutIDs(stdout.String()) != want {
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
	if withoutIDs(stdout.String()) != want {
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
	if withoutIDs(stdout.String()) != want {
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
	if withoutIDs(string(got)) != want {
		t.Errorf("Journal/14-10-2026.md = %q, want %q", got, want)
	}
}

// Week listing spans the last seven daily notes, oldest first, each under a
// "## YYYY-MM-DD" heading; days without captures (or without a file) print
// nothing, a day seven back is the far edge included, and anything older is
// excluded. Nothing is created or changed.
func TestRunWeekListSpansSevenDaysOldestFirst(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	// testClock holds 2026-10-14, so the week covers 08..14 and 07 is out.
	files := map[string]string{
		"2026-10-08.md": "# Daily\n\n- [ ] old task\n- [x] later done\n",
		"2026-10-10.md": "some prose\n\n- 10:00 | mid note\n- 10:05 | second thought\n",
		"2026-10-13.md": "- 08:00 | yesterday note\n- [ ] yesterday task\n",
		"2026-10-14.md": "- 09:15 | today note\n",
		"2026-10-07.md": "- 12:00 | too old, excluded\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(vault, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	before := make(map[string]string)
	for name, content := range files {
		before[name] = content
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"-l", "-w"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(-l -w): %v", err)
	}
	want := "## 2026-10-08\n- [ ] old task\n- [x] later done\n" +
		"## 2026-10-10\n- 10:00 | mid note\n- 10:05 | second thought\n" +
		"## 2026-10-13\n- 08:00 | yesterday note\n- [ ] yesterday task\n" +
		"## 2026-10-14\n- 09:15 | today note\n"
	if withoutIDs(stdout.String()) != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	for name, content := range files {
		got, err := os.ReadFile(filepath.Join(vault, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Errorf("%s changed: %q, want %q", name, got, content)
		}
	}

	// The long forms resolve to the same run.
	stdout.Reset()
	if err := run([]string{"--list", "--week"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(--list --week): %v", err)
	}
	if withoutIDs(stdout.String()) != want {
		t.Errorf("--list --week stdout = %q, want %q", stdout.String(), want)
	}
}

// A week with no captures anywhere — no files at all, or only prose-only
// Daily notes — prints nothing, exits 0, and creates nothing.
func TestRunWeekListEmptyResults(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"no daily notes at all", nil},
		{"daily notes without capture lines", map[string]string{
			"2026-10-10.md": "# Daily\n\nsome prose\n",
			"2026-10-12.md": "## Tasks\n\n- bring snacks\n",
		}},
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
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(vault, name), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}

			var stdout, stderr bytes.Buffer
			if err := run([]string{"-l", "-w"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Fatalf("run(-l -w): %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("want empty output, got stdout=%q stderr=%q", stdout.String(), stderr.String())
			}

			entries, err := os.ReadDir(vault)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != len(tc.files) {
				t.Errorf("vault holds %d entries (%v), want %d: nothing may be created", len(entries), entries, len(tc.files))
			}
		})
	}
}

// The week listing honours CAPTURE_DAILY_DIR and CAPTURE_DAILY_FORMAT: it
// reads the files a capture appends to, across the whole range.
func TestRunWeekHonoursCustomDailyLayout(t *testing.T) {
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
	for name, content := range map[string]string{
		"08-10-2026.md": "- 10:00 | week-old note\n",
		"13-10-2026.md": "- 08:00 | yesterday note\n",
		"14-10-2026.md": "- 09:15 | today note\n",
		"07-10-2026.md": "- 12:00 | too old, excluded\n",
	} {
		if err := os.WriteFile(filepath.Join(vault, "Journal", name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"-l", "-w"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(-l -w): %v", err)
	}
	want := "## 2026-10-08\n- 10:00 | week-old note\n" +
		"## 2026-10-13\n- 08:00 | yesterday note\n" +
		"## 2026-10-14\n- 09:15 | today note\n"
	if withoutIDs(stdout.String()) != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if strings.Contains(stdout.String(), "too old") {
		t.Errorf("stdout includes captures older than the week: %q", stdout.String())
	}
}

// Week is a modifier of list mode: without -l it fails loudly and creates
// nothing; with it, the usual list rules hold and capture input is rejected.
func TestRunWeekModeGuards(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string // substring of the expected error
	}{
		{"-w alone", []string{"-w"}, "", "requires -l"},
		{"--week alone", []string{"--week"}, "", "requires -l"},
		{"-w positional text", []string{"-w", "some", "text"}, "", "requires -l"},
		{"-l -w with text", []string{"-l", "-w", "some", "text"}, "", "no capture input"},
		{"-l -w with stdin", []string{"-l", "-w"}, "ideas\n", "no capture input"},
		{"-l -w with -t", []string{"-l", "-w", "-t"}, "", "no capture input"},
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

			var stdout, stderr bytes.Buffer
			err := run(tc.args, getenv, testClock, strings.NewReader(tc.stdin), &stdout, &stderr)
			if err == nil {
				t.Fatalf("run(%v) returned nil, want error", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if _, statErr := os.Stat(filepath.Join(vault, "2026-10-14.md")); !os.IsNotExist(statErr) {
				t.Error("a failing week invocation must not create the Daily note")
			}
		})
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

// recordingOpener captures the URI run passes to the system opener.
type recordingOpener struct {
	urls []string
	err  error
}

func (o *recordingOpener) open(rawURL string) error {
	o.urls = append(o.urls, rawURL)
	return o.err
}

func expectedObsidianURL(t *testing.T, path string) string {
	t.Helper()
	q := url.Values{}
	q.Set("path", path)
	return "obsidian://open?" + q.Encode()
}

// `capture open` opens today's Daily note in Obsidian, creating an empty note
// first, and never reads a terminal stdin or writes output.
func TestRunOpenLaunchesTodaysNote(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	stdin := newStatReader(os.ModeCharDevice, "")
	opener := &recordingOpener{}

	var stdout, stderr bytes.Buffer
	if err := runWithOpener([]string{"open"}, getenv, testClock, stdin, &stdout, &stderr, opener.open); err != nil {
		t.Fatalf("run(open): %v", err)
	}
	if stdin.read {
		t.Error("open read a terminal stdin; it must never block waiting for EOF")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("open must be silent, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	path := filepath.Join(vault, "2026-10-14.md")
	wantURL := expectedObsidianURL(t, path)
	if len(opener.urls) != 1 || opener.urls[0] != wantURL {
		t.Errorf("opened %v, want exactly [%s]", opener.urls, wantURL)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("open must create the missing Daily note: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("new Daily note = %q, want empty", got)
	}
}

// Open resolves the same Daily note as a capture: CAPTURE_DAILY_DIR and
// CAPTURE_DAILY_FORMAT both apply, and an existing note is left untouched.
func TestRunOpenHonoursLayoutAndPreservesContent(t *testing.T) {
	vault := t.TempDir()
	if err := os.Mkdir(filepath.Join(vault, "Journal"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vault, "Journal", "14-10-2026.md")
	content := "# Daily\n\n- 08:00 | earlier\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
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
	opener := &recordingOpener{}

	var stdout, stderr bytes.Buffer
	if err := runWithOpener([]string{"open"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr, opener.open); err != nil {
		t.Fatalf("run(open): %v", err)
	}
	wantURL := expectedObsidianURL(t, path)
	if len(opener.urls) != 1 || opener.urls[0] != wantURL {
		t.Errorf("opened %v, want exactly [%s]", opener.urls, wantURL)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("open modified the Daily note: %q, want %q", got, content)
	}
}

// Open is a command, not a capture: flags, text and piped stdin are all
// rejected loudly and nothing is created or launched.
func TestRunOpenRejectsCaptureInput(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
	}{
		{"positional text", []string{"open", "some", "text"}, ""},
		{"task flag", []string{"open", "-t"}, ""},
		{"list flag", []string{"open", "-l"}, ""},
		{"week flag", []string{"open", "-w"}, ""},
		{"piped stdin", []string{"open"}, "ideas\n"},
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
			opener := &recordingOpener{}

			var stdout, stderr bytes.Buffer
			err := runWithOpener(tc.args, getenv, testClock, strings.NewReader(tc.stdin), &stdout, &stderr, opener.open)
			if err == nil {
				t.Fatalf("run(%v) returned nil, want error", tc.args)
			}
			if !strings.Contains(err.Error(), "open takes no") {
				t.Errorf("error = %q, want it to reject open input", err.Error())
			}
			if len(opener.urls) != 0 {
				t.Errorf("a failing open launched %v", opener.urls)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if _, statErr := os.Stat(filepath.Join(vault, "2026-10-14.md")); !os.IsNotExist(statErr) {
				t.Error("a failing open must not create the Daily note")
			}
		})
	}
}

// The word "open" is still capturable through the documented escape hatches,
// and a flag before it keeps it plain text.
func TestRunOpenWordRemainsCapturable(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"-- separator", []string{"--", "open"}, "- 09:15 | open\n"},
		{"task flag before", []string{"-t", "open"}, "- [ ] | 09:15 | open\n"},
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
			var stdout, stderr bytes.Buffer
			if err := run(tc.args, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Fatalf("run(%v): %v", tc.args, err)
			}
			got, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md"))
			if err != nil {
				t.Fatal(err)
			}
			if withoutIDs(string(got)) != tc.want {
				t.Errorf("2026-10-14.md = %q, want %q", got, tc.want)
			}
		})
	}
}

// A failing opener surfaces its error instead of being swallowed.
func TestRunOpenReportsLauncherError(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	opener := &recordingOpener{err: errors.New("no handler")}

	var stdout, stderr bytes.Buffer
	err := runWithOpener([]string{"open"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr, opener.open)
	if err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Fatalf("error = %v, want the launcher error", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

// Open surfaces the same configuration errors as a capture and creates
// nothing when the vault or daily layout is wrong.
func TestRunOpenConfigErrorsMatchCapture(t *testing.T) {
	cases := []struct {
		name        string
		makeVault   func(t *testing.T) string
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
			opener := &recordingOpener{}

			var captureOut, captureErr, openOut, openErr bytes.Buffer
			errCapture := run([]string{"hello"}, getenv, testClock, strings.NewReader(""), &captureOut, &captureErr)
			errOpen := runWithOpener([]string{"open"}, getenv, testClock, strings.NewReader(""), &openOut, &openErr, opener.open)

			if errCapture == nil || errOpen == nil {
				t.Fatalf("want errors from both legs, got capture=%v open=%v", errCapture, errOpen)
			}
			if errOpen.Error() != errCapture.Error() {
				t.Errorf("open error %q != capture error %q", errOpen.Error(), errCapture.Error())
			}
			if len(opener.urls) != 0 {
				t.Errorf("a failing open launched %v", opener.urls)
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

// The URI always carries an absolute path, and spaces are encoded.
func TestObsidianURLIsAbsoluteAndEncoded(t *testing.T) {
	rel := filepath.Join("relative", "daily note", "2026-10-14.md")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got := obsidianURL(rel)
	want := expectedObsidianURL(t, filepath.Join(cwd, rel))
	if got != want {
		t.Errorf("obsidianURL(%q) = %q, want %q", rel, got, want)
	}
}

// Find prints the matching capture with its location, across days, oldest
// daily note first. An id quoted inside another capture's text is not a column
// hit, and a Markdown file that is not a daily note is never read.
func TestRunFindPrintsMatchingLineWithLocation(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	older := filepath.Join(vault, "2026-10-12.md")
	newer := filepath.Join(vault, "2026-10-14.md")
	olderBody := "# Daily\n\n- 08:00 | a1b2c3d | first mention\n- [ ] | 08:10 | 0badf00 | task with another id\n"
	newerBody := "- 09:00 | a1b2c3d | second mention\n- [x] | 09:05 | a1b2c3d | checked task, same id\n" +
		"- 09:10 | deadbee | prose quoting  | a1b2c3d |  is not an id column\n"
	if err := os.WriteFile(older, []byte(olderBody), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte(newerBody), 0644); err != nil {
		t.Fatal(err)
	}
	// Not a daily note under the default layout: must not be searched.
	if err := os.WriteFile(filepath.Join(vault, "notes.md"), []byte("- 07:00 | a1b2c3d | stray\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"find", "a1b2c3d"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(find): %v", err)
	}
	want := older + ":3:- 08:00 | a1b2c3d | first mention\n" +
		newer + ":1:- 09:00 | a1b2c3d | second mention\n" +
		newer + ":2:- [x] | 09:05 | a1b2c3d | checked task, same id\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if got, err := os.ReadFile(newer); err != nil || string(got) != newerBody {
		t.Errorf("find modified the Daily note: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(vault, "notes.md")); err != nil ||
		string(got) != "- 07:00 | a1b2c3d | stray\n" {
		t.Errorf("find touched the non-daily note: %q, %v", got, err)
	}
}

// A missing id, an empty vault, and an id that only appears inside another
// capture's text are all empty results: no output, no error, nothing created.
func TestRunFindEmptyResults(t *testing.T) {
	cases := []struct {
		name string
		note string // "" = no daily note exists
		id   string
	}{
		{"missing daily note", "", "a1b2c3d"},
		{"daily note without a match", "- 09:00 | 0badf00 | other\n", "a1b2c3d"},
		{"id only inside another capture's text", "- 09:00 | deadbee | saw | a1b2c3d | earlier\n", "a1b2c3d"},
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
			if tc.note != "" {
				if err := os.WriteFile(filepath.Join(vault, "2026-10-14.md"), []byte(tc.note), 0644); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			if err := run([]string{"find", tc.id}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Fatalf("run(find %s): %v", tc.id, err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
			}
		})
	}
}

// Find refuses anything that is not one valid id, before it reads the vault,
// and rejects piped input like the other read-only commands.
func TestRunFindRejectsCaptureInput(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"no id", []string{"find"}, "", "exactly one capture id"},
		{"two ids", []string{"find", "a1b2c3d", "0badf00"}, "", "exactly one capture id"},
		{"list flag", []string{"find", "-l", "a1b2c3d"}, "", "exactly one capture id"},
		{"task flag", []string{"find", "-t", "a1b2c3d"}, "", "exactly one capture id"},
		{"uppercase id", []string{"find", "A1B2C3D"}, "", "not a capture id"},
		{"short id", []string{"find", "a1b2c3"}, "", "not a capture id"},
		{"long id", []string{"find", "a1b2c3d4"}, "", "not a capture id"},
		{"non-hex id", []string{"find", "zzzzzzz"}, "", "not a capture id"},
		{"piped stdin", []string{"find", "a1b2c3d"}, "piped\n", "find takes no stdin"},
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
			var stdout, stderr bytes.Buffer
			err := run(tc.args, getenv, testClock, strings.NewReader(tc.stdin), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if entries, readErr := os.ReadDir(vault); readErr != nil || len(entries) != 0 {
				t.Errorf("rejected find touched the vault: %v, %v", entries, readErr)
			}
		})
	}
}

// Find surfaces the same configuration errors as a capture and requires
// nothing beyond them: a missing vault fails, an unset daily dir is fine.
func TestRunFindConfigErrorsMatchCapture(t *testing.T) {
	cases := []struct {
		name      string
		vaultMode string // "unset", "file", "dir"
		dailyDir  string
	}{
		{"unset CAPTURE_VAULT", "unset", ""},
		{"CAPTURE_VAULT not a directory", "file", ""},
		{"missing CAPTURE_DAILY_DIR", "dir", "Missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := ""
			if tc.vaultMode != "unset" {
				vault = filepath.Join(t.TempDir(), "vault")
				if tc.vaultMode == "file" {
					if err := os.WriteFile(vault, []byte("x"), 0644); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Mkdir(vault, 0755); err != nil {
					t.Fatal(err)
				}
			}
			getenv := func(key string) string {
				switch key {
				case "CAPTURE_VAULT":
					return vault
				case "CAPTURE_DAILY_DIR":
					return tc.dailyDir
				}
				return ""
			}
			var stdout, stderr bytes.Buffer
			err := run([]string{"find", "a1b2c3d"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr)
			if err == nil {
				t.Fatal("find succeeded with bad configuration")
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

// Find honours CAPTURE_DAILY_FORMAT: only files named by that layout are read.
func TestRunFindHonoursCustomDailyLayout(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		switch key {
		case "CAPTURE_VAULT":
			return vault
		case "CAPTURE_DAILY_FORMAT":
			return "02-01-2006"
		}
		return ""
	}
	custom := filepath.Join(vault, "14-10-2026.md")
	if err := os.WriteFile(custom, []byte("- 09:00 | a1b2c3d | custom layout\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Default-layout name is not a daily note under the configured layout.
	if err := os.WriteFile(filepath.Join(vault, "2026-10-14.md"), []byte("- 09:00 | a1b2c3d | ignored\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"find", "a1b2c3d"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(find): %v", err)
	}
	want := custom + ":1:- 09:00 | a1b2c3d | custom layout\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

// Find stays a command: the word itself is still capturable with -- or a flag.
func TestRunFindWordRemainsCapturable(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"-- separator", []string{"--", "find", "a1b2c3d"}, "- 09:15 | find a1b2c3d\n"},
		{"task flag before", []string{"-t", "find", "a1b2c3d"}, "- [ ] | 09:15 | find a1b2c3d\n"},
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
			var stdout, stderr bytes.Buffer
			if err := run(tc.args, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Fatalf("run(%v): %v", tc.args, err)
			}
			got, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md"))
			if err != nil {
				t.Fatal(err)
			}
			if withoutIDs(string(got)) != tc.want {
				t.Errorf("2026-10-14.md = %q, want %q", got, tc.want)
			}
		})
	}
}

// A Move drains the Previous daily note of its unfinished Tasks: they arrive in
// today's note in source order, verbatim, and leave the source; checked Tasks,
// Notes, prose, headings and nested checkboxes stay where they are. Today's
// existing content is kept and its missing trailing newline completed first.
func TestRunMoveDrainsUnfinishedTasksIntoToday(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	source := filepath.Join(vault, "2026-10-12.md")
	sourceBody := "# Daily 2026-10-12\n" +
		"\n" +
		"- 08:00 | a1b2c3d | morning note\n" +
		"- [ ] | 08:10 | 0badf00 | unfinished task\n" +
		"- [x] | 08:20 | 1c2d3e4 | checked task\n" +
		"- [X] | 08:25 | 2d3e4f5 | checked uppercase task\n" +
		"  - [ ] | 08:30 | 3e4f5a6 | nested task\n" +
		"- [ ] | 08:40 | 4f5a6b7 | second unfinished\n" +
		"plain prose, not a capture\n" +
		// A hand-written line opening with the task prefix is movable too, so
		// parking an idea needs another shape than an unchecked box.
		"- [ ] hand-written task without columns\n"
	if err := os.WriteFile(source, []byte(sourceBody), 0644); err != nil {
		t.Fatal(err)
	}
	today := filepath.Join(vault, "2026-10-14.md")
	// No trailing newline: the append must complete it before the moved lines.
	if err := os.WriteFile(today, []byte("- 07:00 | c48e5f0 | earlier today"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(move): %v", err)
	}
	wantStdout := "- [ ] | 08:10 | 0badf00 | unfinished task\n" +
		"- [ ] | 08:40 | 4f5a6b7 | second unfinished\n" +
		"- [ ] hand-written task without columns\n"
	if stdout.String() != wantStdout {
		t.Errorf("stdout = %q, want %q", stdout.String(), wantStdout)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	wantToday := "- 07:00 | c48e5f0 | earlier today\n" + wantStdout
	if got, err := os.ReadFile(today); err != nil || string(got) != wantToday {
		t.Errorf("today's note = %q, %v; want %q", got, err, wantToday)
	}
	wantSource := "# Daily 2026-10-12\n" +
		"\n" +
		"- 08:00 | a1b2c3d | morning note\n" +
		"- [x] | 08:20 | 1c2d3e4 | checked task\n" +
		"- [X] | 08:25 | 2d3e4f5 | checked uppercase task\n" +
		"  - [ ] | 08:30 | 3e4f5a6 | nested task\n" +
		"plain prose, not a capture\n"
	if got, err := os.ReadFile(source); err != nil || string(got) != wantSource {
		t.Errorf("source = %q, %v; want %q", got, err, wantSource)
	}
}

// A moved Task keeps its id and capture time, so find calls it back from its
// new day and no longer from the source.
func TestRunMoveKeepsCaptureIDAndTime(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	task := "- [ ] | 08:10 | 0badf00 | call the dentist\n"
	source := filepath.Join(vault, "2026-10-13.md")
	if err := os.WriteFile(source, []byte("# 13th\n"+task), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(move): %v", err)
	}
	if stdout.String() != task {
		t.Errorf("stdout = %q, want %q", stdout.String(), task)
	}

	stdout.Reset()
	if err := run([]string{"find", "0badf00"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(find): %v", err)
	}
	want := filepath.Join(vault, "2026-10-14.md") + ":1:" + strings.TrimSuffix(task, "\n") + "\n"
	if stdout.String() != want {
		t.Errorf("find after move = %q, want %q", stdout.String(), want)
	}
}

// Running twice changes nothing the second time: the source has no unfinished
// Task left, so the second Move is silent and both notes are untouched.
func TestRunMoveSecondRunIsNoOp(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	source := filepath.Join(vault, "2026-10-13.md")
	sourceBody := "# Daily\n- [ ] | 08:10 | 0badf00 | move me\n- 08:20 | e02f7d9 | stay here\n"
	if err := os.WriteFile(source, []byte(sourceBody), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("first run(move): %v", err)
	}
	today := filepath.Join(vault, "2026-10-14.md")
	wantSource := "# Daily\n- 08:20 | e02f7d9 | stay here\n"
	wantToday := "- [ ] | 08:10 | 0badf00 | move me\n"
	if got, err := os.ReadFile(source); err != nil || string(got) != wantSource {
		t.Fatalf("source after first move = %q, %v; want %q", got, err, wantSource)
	}
	if got, err := os.ReadFile(today); err != nil || string(got) != wantToday {
		t.Fatalf("today after first move = %q, %v; want %q", got, err, wantToday)
	}

	stdout.Reset()
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("second run(move): %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("second run stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != wantSource {
		t.Errorf("second run changed the source: %q, %v", got, err)
	}
	if got, err := os.ReadFile(today); err != nil || string(got) != wantToday {
		t.Errorf("second run changed today's note: %q, %v", got, err)
	}
}

// No Previous daily note, a daily note with nothing movable, or only a
// future-dated daily note are all empty results: no output, exit 0, no file
// created for today. Exactly one daily note is read, so a newer one without
// Tasks hides an older one that holds them.
func TestRunMoveEmptyResults(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{
			name:  "no previous daily note",
			files: map[string]string{},
		},
		{
			name: "previous daily note holds nothing movable",
			files: map[string]string{
				"2026-10-13.md": "# Daily\n\n- 08:00 | a1b2c3d | a note\n- [x] | 08:10 | 0badf00 | done\n",
			},
		},
		{
			name: "only a future-dated daily note exists",
			files: map[string]string{
				"2026-10-20.md": "- [ ] | 08:10 | 0badf00 | planned ahead\n",
			},
		},
		{
			name: "newest older daily note has no tasks, an older one has",
			files: map[string]string{
				"2026-10-10.md": "- [ ] | 08:00 | 1c2d3e4 | stranded older task\n",
				"2026-10-13.md": "- 08:00 | a1b2c3d | only a note\n",
			},
		},
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
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(vault, name), []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}

			var stdout, stderr bytes.Buffer
			if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Fatalf("run(move): %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
			}
			for name, body := range tc.files {
				if got, err := os.ReadFile(filepath.Join(vault, name)); err != nil || string(got) != body {
					t.Errorf("%s changed: %q, %v; want %q", name, got, err, body)
				}
			}
			if _, err := os.Stat(filepath.Join(vault, "2026-10-14.md")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("today's note was created, want none: %v", err)
			}
			entries, err := os.ReadDir(vault)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != len(tc.files) {
				t.Errorf("vault entries = %d, want %d (no leftovers)", len(entries), len(tc.files))
			}
		})
	}
}

// The source is the newest Daily note dated before today, however old: a
// future-dated daily note and any daily note older than the newest one before
// today are left alone, and today's own Task is never a source.
func TestRunMoveSelectsNewestDailyNoteBeforeToday(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	files := map[string]string{
		"2026-09-23.md": "- [ ] | 07:00 | 5a1c8b3 | three weeks old\n",
		"2026-10-12.md": "# 12th\n- [ ] | 08:10 | 0badf00 | move me\n- 08:20 | e02f7d9 | note stays\n",
		"2026-10-14.md": "- [ ] | 07:30 | 7f7f7f7 | today's own task\n",
		"2026-10-20.md": "- [ ] | 08:00 | 4f5a6b7 | future plan\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(vault, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// An old note edited today must not be promoted by its modification time.
	touched := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(vault, "2026-09-23.md"), touched, touched); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(move): %v", err)
	}
	moved := "- [ ] | 08:10 | 0badf00 | move me\n"
	if stdout.String() != moved {
		t.Errorf("stdout = %q, want %q", stdout.String(), moved)
	}
	if got, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md")); err != nil || string(got) != files["2026-10-14.md"]+moved {
		t.Errorf("today's note = %q, %v; want the moved line appended", got, err)
	}
	wantSource := "# 12th\n- 08:20 | e02f7d9 | note stays\n"
	if got, err := os.ReadFile(filepath.Join(vault, "2026-10-12.md")); err != nil || string(got) != wantSource {
		t.Errorf("source = %q, %v; want %q", got, err, wantSource)
	}
	for _, name := range []string{"2026-09-23.md", "2026-10-20.md"} {
		if got, err := os.ReadFile(filepath.Join(vault, name)); err != nil || string(got) != files[name] {
			t.Errorf("%s changed: %q, %v", name, got, err)
		}
	}
}

// Filenames are parsed, not sorted as strings: with a layout whose month comes
// first, string order would pick September over October.
func TestRunMoveParsesFilenamesNotStringOrder(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		switch key {
		case "CAPTURE_VAULT":
			return vault
		case "CAPTURE_DAILY_FORMAT":
			return "January 2, 2006"
		}
		return ""
	}
	older := "- [ ] | 07:00 | 5a1c8b3 | september task\n"
	newer := "- [ ] | 08:10 | 0badf00 | october task\n"
	if err := os.WriteFile(filepath.Join(vault, "September 30, 2026.md"), []byte(older), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "October 12, 2026.md"), []byte(newer), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(move): %v", err)
	}
	if stdout.String() != newer {
		t.Errorf("stdout = %q, want the October task only", stdout.String())
	}
	if got, err := os.ReadFile(filepath.Join(vault, "September 30, 2026.md")); err != nil || string(got) != older {
		t.Errorf("September note changed: %q, %v", got, err)
	}
	wantToday := "October 14, 2026.md"
	if got, err := os.ReadFile(filepath.Join(vault, wantToday)); err != nil || string(got) != newer {
		t.Errorf("%s = %q, %v; want %q", wantToday, got, err, newer)
	}
}

// Two daily notes dated the same day tie on the greater filename, not on which
// one happens to be newer on disk.
func TestRunMoveTieOnOneDateGoesToGreaterFilename(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		switch key {
		case "CAPTURE_VAULT":
			return vault
		case "CAPTURE_DAILY_FORMAT":
			return "January 2, 2006 15"
		}
		return ""
	}
	earlier := filepath.Join(vault, "October 12, 2026 08.md")
	later := filepath.Join(vault, "October 12, 2026 09.md")
	earlierBody := "- [ ] | 07:00 | 5a1c8b3 | earlier same day\n"
	laterBody := "- [ ] | 08:10 | 0badf00 | later same day\n"
	if err := os.WriteFile(earlier, []byte(earlierBody), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(later, []byte(laterBody), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(move): %v", err)
	}
	if stdout.String() != laterBody {
		t.Errorf("stdout = %q, want the greater filename's task", stdout.String())
	}
	if got, err := os.ReadFile(filepath.Join(vault, "October 14, 2026 09.md")); err != nil || string(got) != laterBody {
		t.Errorf("today's note = %q, %v; want the greater filename's task", got, err)
	}
	if got, err := os.ReadFile(earlier); err != nil || string(got) != earlierBody {
		t.Errorf("lesser filename changed: %q, %v", got, err)
	}
	// The greater filename is the source, so its task left it behind.
	if got, err := os.ReadFile(later); err != nil || len(got) != 0 {
		t.Errorf("greater filename = %q, %v; want an existing empty file", got, err)
	}
}

// Monday's Move reaches back over a skipped Sunday to Saturday's note, and no
// Sunday file is required or created.
func TestRunMoveSpansWeekend(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	monday := time.Date(2026, 10, 19, 9, 15, 0, 0, time.FixedZone("UTC+7", 7*3600))
	saturday := "- [ ] | 10:00 | 5a1c8b3 | saturday errand\n- [x] | 11:00 | 1c2d3e4 | saturday done\n"
	if err := os.WriteFile(filepath.Join(vault, "2026-10-17.md"), []byte(saturday), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, monday, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(move): %v", err)
	}
	moved := "- [ ] | 10:00 | 5a1c8b3 | saturday errand\n"
	if stdout.String() != moved {
		t.Errorf("stdout = %q, want %q", stdout.String(), moved)
	}
	if got, err := os.ReadFile(filepath.Join(vault, "2026-10-19.md")); err != nil || string(got) != moved {
		t.Errorf("Monday's note = %q, %v; want %q", got, err, moved)
	}
	wantSource := "- [x] | 11:00 | 1c2d3e4 | saturday done\n"
	if got, err := os.ReadFile(filepath.Join(vault, "2026-10-17.md")); err != nil || string(got) != wantSource {
		t.Errorf("Saturday's note = %q, %v; want %q", got, err, wantSource)
	}
	for _, name := range []string{"2026-10-18.md", "2026-10-20.md"} {
		if _, err := os.Stat(filepath.Join(vault, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s was created, want none: %v", name, err)
		}
	}
}

// A source the Move empties stays as an empty file: notes are never deleted.
func TestRunMoveEmptiedSourceRemainsAnEmptyFile(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	source := filepath.Join(vault, "2026-10-13.md")
	moved := "- [ ] | 08:10 | 0badf00 | last task of the day\n"
	if err := os.WriteFile(source, []byte(moved), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(move): %v", err)
	}
	if got, err := os.ReadFile(source); err != nil || len(got) != 0 {
		t.Errorf("source = %q, %v; want an existing empty file", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md")); err != nil || string(got) != moved {
		t.Errorf("today's note = %q, %v; want %q", got, err, moved)
	}
}

// A source without a trailing newline is preserved as it was: removing a line
// never adds a terminator to the last line left behind, and a moved final line
// still arrives complete on its own line.
func TestRunMovePreservesMissingTrailingNewline(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		wantSource string
		wantToday  string
	}{
		{
			name:       "moved task is the last line without a newline",
			source:     "- 08:00 | a1b2c3d | a note\n- [ ] | 08:10 | 0badf00 | last task",
			wantSource: "- 08:00 | a1b2c3d | a note\n",
			wantToday:  "- [ ] | 08:10 | 0badf00 | last task\n",
		},
		{
			name:       "kept note is the last line without a newline",
			source:     "- [ ] | 08:10 | 0badf00 | a task\n- 08:20 | e02f7d9 | last note",
			wantSource: "- 08:20 | e02f7d9 | last note",
			wantToday:  "- [ ] | 08:10 | 0badf00 | a task\n",
		},
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
			source := filepath.Join(vault, "2026-10-13.md")
			if err := os.WriteFile(source, []byte(tc.source), 0644); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Fatalf("run(move): %v", err)
			}
			if got, err := os.ReadFile(source); err != nil || string(got) != tc.wantSource {
				t.Errorf("source = %q, %v; want %q", got, err, tc.wantSource)
			}
			if got, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md")); err != nil || string(got) != tc.wantToday {
				t.Errorf("today's note = %q, %v; want %q", got, err, tc.wantToday)
			}
		})
	}
}

// CAPTURE_DAILY_FORMAT names both the file read and the file written; a
// default-layout name is not a daily note under the configured layout.
func TestRunMoveHonoursCustomDailyLayout(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		switch key {
		case "CAPTURE_VAULT":
			return vault
		case "CAPTURE_DAILY_FORMAT":
			return "02-01-2006"
		}
		return ""
	}
	stray := "- [ ] | 08:10 | 0badf00 | wrong layout, never a source\n"
	if err := os.WriteFile(filepath.Join(vault, "2026-10-13.md"), []byte(stray), 0644); err != nil {
		t.Fatal(err)
	}
	moved := "- [ ] | 09:10 | 1c2d3e4 | custom layout task\n"
	if err := os.WriteFile(filepath.Join(vault, "13-10-2026.md"), []byte(moved), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(move): %v", err)
	}
	if stdout.String() != moved {
		t.Errorf("stdout = %q, want %q", stdout.String(), moved)
	}
	if got, err := os.ReadFile(filepath.Join(vault, "14-10-2026.md")); err != nil || string(got) != moved {
		t.Errorf("14-10-2026.md = %q, %v; want %q", got, err, moved)
	}
	if _, err := os.Stat(filepath.Join(vault, "2026-10-14.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("default-layout today's note was created: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(vault, "2026-10-13.md")); err != nil || string(got) != stray {
		t.Errorf("wrong-layout note changed: %q, %v", got, err)
	}
}

// Move refuses flags, text and piped stdin before it touches the vault.
func TestRunMoveRejectsCaptureInput(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"task flag", []string{"move", "-t"}, "", "move takes no flags, text, or stdin"},
		{"list flag", []string{"move", "-l"}, "", "move takes no flags, text, or stdin"},
		{"week flag", []string{"move", "-w"}, "", "move takes no flags, text, or stdin"},
		{"positional text", []string{"move", "later"}, "", "move takes no flags, text, or stdin"},
		{"flag and text", []string{"move", "-t", "later"}, "", "move takes no flags, text, or stdin"},
		{"piped stdin", []string{"move"}, "piped\n", "move takes no stdin"},
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
			var stdout, stderr bytes.Buffer
			err := run(tc.args, getenv, testClock, strings.NewReader(tc.stdin), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if entries, readErr := os.ReadDir(vault); readErr != nil || len(entries) != 0 {
				t.Errorf("rejected move touched the vault: %v, %v", entries, readErr)
			}
		})
	}
}

// Today's note is appended to before the source is rewritten: when the append
// fails, the source still holds its unfinished Task.
func TestRunMoveLeavesSourceIntactWhenAppendFails(t *testing.T) {
	vault := t.TempDir()
	getenv := func(key string) string {
		if key == "CAPTURE_VAULT" {
			return vault
		}
		return ""
	}
	source := filepath.Join(vault, "2026-10-13.md")
	sourceBody := "- [ ] | 08:10 | 0badf00 | keep me\n"
	if err := os.WriteFile(source, []byte(sourceBody), 0644); err != nil {
		t.Fatal(err)
	}
	// A directory where today's note should be makes the append fail.
	if err := os.Mkdir(filepath.Join(vault, "2026-10-14.md"), 0755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"move"}, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("move succeeded although today's note could not be written")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != sourceBody {
		t.Errorf("source = %q, %v; want it unchanged", got, err)
	}
}

// Move stays a command: the word itself is still capturable with -- or a flag.
func TestRunMoveWordRemainsCapturable(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"-- separator", []string{"--", "move"}, "- 09:15 | move\n"},
		{"task flag before", []string{"-t", "move"}, "- [ ] | 09:15 | move\n"},
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
			var stdout, stderr bytes.Buffer
			if err := run(tc.args, getenv, testClock, strings.NewReader(""), &stdout, &stderr); err != nil {
				t.Fatalf("run(%v): %v", tc.args, err)
			}
			got, err := os.ReadFile(filepath.Join(vault, "2026-10-14.md"))
			if err != nil {
				t.Fatal(err)
			}
			if withoutIDs(string(got)) != tc.want {
				t.Errorf("2026-10-14.md = %q, want %q", got, tc.want)
			}
		})
	}
}

// --help documents the Move beside open and find.
func TestRunMoveHelpDocumentsCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--help"}, func(string) string { return "" }, testClock,
		strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run(--help) returned %v, want nil", err)
	}
	help := stdout.String()
	for _, want := range []string{"capture move", "previous daily note", "unfinished tasks"} {
		if !strings.Contains(help, want) {
			t.Errorf("--help output missing %q:\n%s", want, help)
		}
	}
}
