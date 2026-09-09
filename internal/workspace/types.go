package workspace

import "context"

type Entry struct {
	Path         string
	OriginalPath string
	Code         string
}

type Status struct {
	Root         string
	Branch       string
	IsRepository bool
	Entries      []Entry
}

type Diff struct {
	Text         string
	IsRepository bool
	// Truncated also marks incomplete coverage from a partial conversation baseline.
	Truncated bool
}

type Baseline interface {
	Diff(context.Context) (Diff, error)
	Close() error
}
