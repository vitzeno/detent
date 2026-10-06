package event

// DiffLoaded answers LoadDiff with the changes from Base to Head, or why there
// are none. Never stored: the trees are the record, and a patch can be large.
type DiffLoaded struct {
	fact
	Base  string     `json:"Base"`
	Head  string     `json:"Head"` // as asked, "" meaning the files when it was asked
	Files []FileDiff `json:"-"`
	Cut   bool       `json:"Cut"` // the patch passed its limit, so files after the last are missing
	Err   string     `json:"Err"`
}

func (DiffLoaded) Kind() Kind { return DiffLoadedKind }

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
