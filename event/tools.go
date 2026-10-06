package event

// The built-in tools' names, which the engine, the UI and the tools all match
// on. MCP tools add names at run time, so a call's Tool stays a string.
const (
	ToolBash          = "bash"
	ToolPowerShell    = "powershell"
	ToolReadFile      = "read_file"
	ToolWriteFile     = "write_file"
	ToolEditFile      = "edit_file"
	ToolListDir       = "list_dir"
	ToolGrep          = "grep"
	ToolFindFiles     = "find_files"
	ToolWebSearch     = "web_search"
	ToolSkill         = "skill"
	ToolSpawnAgent    = "spawn_agent"
	ToolReviewDiff    = "review_diff"
	ToolReviewComment = "review_comment"
)
