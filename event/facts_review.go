package event

import "github.com/google/uuid"

// DiffLoaded answers LoadDiff with the changes from Base to Head, or why there
// are none. Never stored: the trees are the record, and a patch can be large.
type DiffLoaded struct {
	fact
	Base string `json:"Base"`
	Head string `json:"Head"` // as asked, "" meaning the files when it was asked
	// Branch echoes the ask, and Against is the ref compared with, Base then
	// being where the branch left it.
	Branch  bool       `json:"Branch"`
	Against string     `json:"Against"`
	Files   []FileDiff `json:"-"`
	Cut     bool       `json:"Cut"` // past its limit, so the last files are missing
	Err     string     `json:"Err"`
}

func (DiffLoaded) Kind() Kind { return DiffLoadedKind }

// ReviewStarted says a reviewer began on a review, with what it reviews, so
// the review can be found again before it has made a single comment.
type ReviewStarted struct {
	fact
	Review   uuid.UUID   `json:"Review"`
	Reviewed uuid.UUID   `json:"Reviewed"`
	Scope    ReviewScope `json:"Scope"`
	Base     string      `json:"Base"`
	Head     string      `json:"Head"`
	Against  string      `json:"Against"`
	Files    int         `json:"Files"` // how many files its diff holds
}

func (ReviewStarted) Kind() Kind { return ReviewStartedKind }

// ReviewCommented is one change to a review's comments. Every one is stored, so
// a resumed session reopens its reviews as they were left.
type ReviewCommented struct {
	fact
	Review   uuid.UUID     `json:"Review"`
	Reviewed uuid.UUID     `json:"Reviewed"` // the request whose changes are under review
	Base     string        `json:"Base"`
	Head     string        `json:"Head"`
	Scope    ReviewScope   `json:"Scope"`
	Against  string        `json:"Against"` // the ref a branch is compared with
	Op       CommentOp     `json:"Op"`
	Comment  ReviewComment `json:"Comment"`
}

func (ReviewCommented) Kind() Kind { return ReviewCommentedKind }

// ReviewSubmitted says a review was sent to the agent, which closes it.
type ReviewSubmitted struct {
	fact
	Review   uuid.UUID `json:"Review"`
	Comments int       `json:"Comments"`
}

func (ReviewSubmitted) Kind() Kind { return ReviewSubmittedKind }

// ReviewComment is one comment on a diff. It quotes the lines it is about, so
// it still reads once git has pruned the trees they came from.
type ReviewComment struct {
	ID      uuid.UUID `json:"ID"`
	ReplyTo uuid.UUID `json:"ReplyTo"`
	Author  string    `json:"Author"` // the agent that wrote it, "" for the human
	Path    string    `json:"Path"`
	Side    string    `json:"Side"` // "old" when every line is a removed one, else "new"
	Start   int       `json:"Start"`
	End     int       `json:"End"`
	Quote   string    `json:"Quote"` // the lines commented on, with their markers
	Body    string    `json:"Body"`
	// Original is a reviewer's words the human rewrote, which made it theirs.
	Original string `json:"Original"`
}

// CommentOp is what a ReviewCommented does to its comment.
type CommentOp string

const (
	CommentAdded   CommentOp = "add"
	CommentEdited  CommentOp = "edit"
	CommentDeleted CommentOp = "delete"
)

// ReviewScope is which changes a review is of.
type ReviewScope string

const (
	ScopeRequest ReviewScope = "request" // one request's
	ScopeSession ReviewScope = "session" // every request's, together
	ScopeSince   ReviewScope = "since"   // the human's own, since the last request ended
	ScopeBranch  ReviewScope = "branch"  // the branch's against the one it left, uncommitted too
)

// FileDiff is one file's changes, parsed once so nothing else reads a patch.
type FileDiff struct {
	Path   string
	Change FileChange
	Binary bool // listed, never drawn or commented on
	Cut    bool // too long to draw, so listed without its hunks
	Hunks  []Hunk
}

// FileChange is what happened to a file as a whole.
type FileChange string

const (
	FileAdded    FileChange = "added"
	FileDeleted  FileChange = "deleted"
	FileModified FileChange = "modified"
)

// Hunk is one @@ section of a file's diff.
type Hunk struct {
	Header string
	Lines  []DiffLine
}

// DiffLine is one line of a hunk, numbered on each side it appears on, so
// Old is 0 for an added line and New is 0 for a removed one.
type DiffLine struct {
	Op   LineOp
	Old  int
	New  int
	Text string // without its marker or newline, so a CRLF line keeps its \r
}

// LineOp is a diff line's marker.
type LineOp byte

const (
	LineContext LineOp = ' '
	LineAdded   LineOp = '+'
	LineRemoved LineOp = '-'
)
