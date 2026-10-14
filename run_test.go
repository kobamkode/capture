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
		name        string
		vaultMode   string // "unset", "missing", "file", "dir"
		args        []string
		now         time.Time
		preExisting *string
		wantErr     string
		wantFile    string
		want        string
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
				if key == "CAPTURE_VAULT" {
					return vaultValue
				}
				return ""
			}

			err := run(tc.args, getenv, now, strings.NewReader(""), &stdout, &stderr)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("run returned nil, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
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
