package main

import (
	"bytes"
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
