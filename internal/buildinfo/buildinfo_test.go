package buildinfo

import "testing"

func TestFormatIncludesCommitWhenSet(t *testing.T) {
	oldVersion := Version
	oldCommit := Commit
	oldBuildDate := BuildDate
	defer func() {
		Version = oldVersion
		Commit = oldCommit
		BuildDate = oldBuildDate
	}()

	Version = "v0.1.0"
	Commit = "abc1234"
	BuildDate = "2026-06-19T00:00:00Z"

	got := Format("sideplane")
	want := "sideplane v0.1.0 (commit abc1234, built 2026-06-19T00:00:00Z)"
	if got != want {
		t.Fatalf("Format() = %q, want %q", got, want)
	}
}

func TestLabelsNormalizeEmptyVersion(t *testing.T) {
	oldVersion := Version
	oldCommit := Commit
	oldBuildDate := BuildDate
	defer func() {
		Version = oldVersion
		Commit = oldCommit
		BuildDate = oldBuildDate
	}()

	Version = ""
	Commit = " abc1234 "
	BuildDate = " 2026-06-19T00:00:00Z "

	version, commit, buildDate := Labels()
	if version != "dev" || commit != "abc1234" || buildDate != "2026-06-19T00:00:00Z" {
		t.Fatalf("Labels() = %q, %q, %q", version, commit, buildDate)
	}
}
