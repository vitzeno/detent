package event

// Status is how a finished tool call went, as the judge reads it.
type Status string

// The statuses. Warnings means it worked but printed something worth a look.
const (
	StatusClean    Status = "clean_success"
	StatusWarnings Status = "success_with_warnings"
	StatusFailed   Status = "failed"
	StatusEmpty    Status = "empty"
)

// Statuses lists every status the judge can publish, pinned to the constants by a test.
func Statuses() []Status {
	return []Status{StatusClean, StatusWarnings, StatusFailed, StatusEmpty}
}
