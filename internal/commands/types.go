package commands

type Command struct {
	Name             string
	Summary          string
	Usage            string
	AllowedWhileBusy bool
}

type Input struct {
	IsCommand bool
	Command   string
	Args      string
	Text      string
}
