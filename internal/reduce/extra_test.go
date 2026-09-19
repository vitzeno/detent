package reduce

import "testing"

func TestPortListenerLines(t *testing.T) {
	tests := []struct {
		name   string
		output string
		golden string
	}{
		{
			name: "real lsof shape, header plus rows",
			output: "COMMAND     PID    USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME\n" +
				"node       4821 mohamed   23u  IPv4 0x951bf105c983071      0t0  TCP *:3000 (LISTEN)\n" +
				"rapportd   1002 mohamed   13u  IPv4 0x951bf105c983072      0t0  TCP *:60480 (LISTEN)\n",
			golden: "testdata/port_listener_lines/basic.golden.json",
		},
		{
			name:   "empty output — nothing listening",
			output: "",
			golden: "testdata/port_listener_lines/empty.golden.json",
		},
		{
			name:   "header only, no listeners",
			output: "COMMAND     PID    USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME\n",
			golden: "testdata/port_listener_lines/header_only.golden.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGolden(t, tt.golden, PortListenerLines(tt.output))
		})
	}
}

func TestDiskUsageLines(t *testing.T) {
	tests := []struct {
		name   string
		output string
		golden string
	}{
		{
			name:   "real du shape, tab-separated",
			output: "8.0K\t./cmd\n4.0K\t./capabilities\n 10M\t./bin\n",
			golden: "testdata/disk_usage_lines/basic.golden.json",
		},
		{
			name:   "empty output",
			output: "",
			golden: "testdata/disk_usage_lines/empty.golden.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGolden(t, tt.golden, DiskUsageLines(tt.output))
		})
	}
}

func TestGitStatusLines(t *testing.T) {
	tests := []struct {
		name   string
		output string
		golden string
	}{
		{
			name:   "real porcelain shape: modified and untracked",
			output: " M .gitignore\n?? Makefile\n?? capabilities/\n",
			golden: "testdata/git_status_lines/basic.golden.json",
		},
		{
			name:   "clean tree",
			output: "",
			golden: "testdata/git_status_lines/empty.golden.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGolden(t, tt.golden, GitStatusLines(tt.output))
		})
	}
}

func TestGitLogLines(t *testing.T) {
	tests := []struct {
		name   string
		output string
		golden string
	}{
		{
			name: "real oneline shape",
			output: "306d245 Add target-resolution feasibility spike\n" +
				"a46b36d Add capability-selection feasibility spike\n",
			golden: "testdata/git_log_lines/basic.golden.json",
		},
		{
			name:   "empty repo, no commits yet",
			output: "",
			golden: "testdata/git_log_lines/empty.golden.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGolden(t, tt.golden, GitLogLines(tt.output))
		})
	}
}
