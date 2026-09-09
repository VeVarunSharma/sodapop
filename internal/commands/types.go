package commands

type Command struct {
	Name             string
	Summary          string
	Usage            string
	AllowedWhileBusy bool
	VendingCategory  string
}

type Input struct {
	IsCommand bool
	Command   string
	Args      string
	Text      string
}
