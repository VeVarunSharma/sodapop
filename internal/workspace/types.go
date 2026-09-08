package workspace

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
	Truncated    bool
}
