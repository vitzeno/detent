package event

// RenderKind is the shape a tool call's output takes, which decides how it
// is drawn. The judge picks one, or a tool declares it in ToolCallProposed.
type RenderKind string

// The render kinds, one per shape of output.
const (
	// RendersText is unstructured text read top to bottom, whatever its length.
	RendersText    RenderKind = "plain_text"
	RendersTable   RenderKind = "table"
	RendersFiles   RenderKind = "file_listing"
	RendersContent RenderKind = "file_content"
	RendersError   RenderKind = "error_text"
	RendersDiff    RenderKind = "diff"
	RendersJSON    RenderKind = "structured_json"
	// RendersMarkdown is only ever declared by a tool, never judged.
	RendersMarkdown RenderKind = "markdown"
)

// RenderKinds lists every render kind, pinned to the constants by a test.
func RenderKinds() []RenderKind {
	return []RenderKind{RendersText, RendersTable, RendersFiles, RendersContent,
		RendersError, RendersDiff, RendersJSON, RendersMarkdown}
}

// Status is how a finished tool call went, as the judge reads it.
type Status string

// The statuses. Warnings means it worked but printed something worth a look.
const (
	StatusClean    Status = "clean_success"
	StatusWarnings Status = "success_with_warnings"
	StatusFailed   Status = "failed"
	StatusEmpty    Status = "empty"
)

// Statuses lists every status the judge can publish, pinned like RenderKinds.
func Statuses() []Status {
	return []Status{StatusClean, StatusWarnings, StatusFailed, StatusEmpty}
}
