package event

// ToolName is what a tool is called, on the wire and in every fact. The
// built-in names are below, and MCP adds names of its own at run time.
type ToolName string

// The built-in tools' names, which the engine, the UI and the tools all match on.
const (
	ToolBash          ToolName = "bash"
	ToolPowerShell    ToolName = "powershell"
	ToolReadFile      ToolName = "read_file"
	ToolWriteFile     ToolName = "write_file"
	ToolEditFile      ToolName = "edit_file"
	ToolListDir       ToolName = "list_dir"
	ToolGrep          ToolName = "grep"
	ToolFindFiles     ToolName = "find_files"
	ToolWebSearch     ToolName = "web_search"
	ToolSkill         ToolName = "skill"
	ToolSpawnAgent    ToolName = "spawn_agent"
	ToolReviewDiff    ToolName = "review_diff"
	ToolReviewComment ToolName = "review_comment"
)
