package reduce

import "testing"

func TestPathLines(t *testing.T) {
	tests := []struct {
		name   string
		output string
		golden string
	}{
		{
			name:   "several files and a directory",
			output: "a.txt\nb.txt\nsub/",
			golden: "testdata/path_lines/basic.golden.json",
		},
		{
			name:   "single file",
			output: "only.txt",
			golden: "testdata/path_lines/single.golden.json",
		},
		{
			name:   "empty output",
			output: "",
			golden: "testdata/path_lines/empty.golden.json",
		},
		{
			name:   "blank lines are skipped, not counted as paths",
			output: "a.txt\n\n\nb.txt\n",
			golden: "testdata/path_lines/blank_lines.golden.json",
		},
		{
			name:   "directory marker stripped from the typed value, kept in facts",
			output: "sub/",
			golden: "testdata/path_lines/directory_only.golden.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGolden(t, tt.golden, PathLines(tt.output))
		})
	}
}
