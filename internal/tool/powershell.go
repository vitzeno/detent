package tool

import "github.com/vitzeno/detent/event"

// PowerShell is Bash where the shell is pwsh, named for what it runs,
// since a model asked to call bash writes bash.
type PowerShell struct{}

func (PowerShell) Name() event.ToolName { return event.ToolPowerShell }

func (PowerShell) Describe() Spec {
	return Spec{
		Description: "Run a PowerShell 7 (pwsh) command: build, test, run, install, git. " +
			"This is PowerShell, not a POSIX shell, so write cmdlets and PowerShell syntax, not bash. " +
			"Not for files: read with read_file, search with grep and find_files, and change a file only with " +
			"edit_file or write_file, never Set-Content, Add-Content, Out-File, New-Item or a > redirect, " +
			"since those two are how the human sees what changed.",
		Params: []Param{
			{Name: "command", Type: TypeString, Desc: "the PowerShell to run", Required: true},
		},
		// Unknown, as bash's is.
		Mutability: "",
	}
}

// Lower is the command as given, as bash's is.
func (PowerShell) Lower(a Args) (string, error) { return Bash{}.Lower(a) }
