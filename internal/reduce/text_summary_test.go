package reduce

import "testing"

func TestTextSummary(t *testing.T) {
	tests := []struct {
		name   string
		output string
		golden string
	}{
		{
			name:   "short multi-line file",
			output: "line one\nline two\nline three\n",
			golden: "testdata/text_summary/short.golden.json",
		},
		{
			name:   "preview caps at 3 lines even when the file has more",
			output: "one\ntwo\nthree\nfour\nfive\n",
			golden: "testdata/text_summary/long.golden.json",
		},
		{
			name:   "empty file",
			output: "",
			golden: "testdata/text_summary/empty.golden.json",
		},
		{
			name:   "single line, no trailing newline",
			output: "no newline here",
			golden: "testdata/text_summary/no_trailing_newline.golden.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGolden(t, tt.golden, TextSummary(tt.output))
		})
	}
}
