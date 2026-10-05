package codex

import "testing"

func TestToolPreview(t *testing.T) {
	for _, c := range []struct {
		name, input, want string
	}{
		{"exec_command", `{"cmd":"go test ./...\necho done","workdir":"/r"}`, "go test ./..."},
		{"apply_patch", "*** Begin Patch\n*** Update File: /r/a.go\n@@\n+x\n*** End Patch\n", "/r/a.go"},
		{"apply_patch", "*** Begin Patch\n*** Add File: /r/a\n+x\n*** Add File: /r/b\n+y\n*** End Patch\n", "2 files"},
		{"apply_patch", "not a patch", ""},
		{"update_plan", `{"plan":[{"step":"Read code","status":"completed"},{"step":"Write tests","status":"in_progress"}]}`, "Write tests"},
		{"update_plan", `{"plan":[{"step":"A","status":"pending"},{"step":"B","status":"pending"}]}`, "2 steps"},
		{"web_search", `{"type":"search","query":"argus codex","queries":["argus codex"]}`, "argus codex"},
		{"view_image", `{"path":"/tmp/x.png","detail":"high"}`, "/tmp/x.png"},
		{"exec", "\n\nconst r = await tools.exec_command({cmd:\"ls\"});\ntext(r.output);\n", "exec_command: ls"},
		{"request_user_input", `{"questions":[{"id":"a","header":"Size","question":"Pick a size","options":[]},{"id":"b","header":"Color","question":"Pick a color"}]}`, "Pick a size +1 more"},
		{"request_user_input", `{"questions":[{"id":"a","header":"Size"}]}`, "Size"},
		{"request_user_input_async", `{"questions":[{"title":"Pick a color","options":["Red"]}]}`, "Pick a color"},
		{"mcp__github__create_issue", `{"title":"x","body":"b"}`, "b"},
		{"mcp__fs__read", `{"path":"/a/b","encoding":"utf8"}`, "/a/b"},
		{"mcp__fs__stat", `{"n":1}`, ""},
		{"unknown_tool", `{"a":1}`, ""},
	} {
		if got := toolPreview(c.name, c.input); got != c.want {
			t.Errorf("toolPreview(%q, %q) = %q, want %q", c.name, c.input, got, c.want)
		}
	}
}

func TestResultIsError(t *testing.T) {
	for _, c := range []struct {
		name, output string
		isErr, known bool
	}{
		{"apply_patch", "Exit code: 1\nWall time: 0.1 seconds\nOutput:\nfailed\n", true, true},
		// A question answers with JSON; any other text is Codex rejecting the call.
		{"request_user_input", `{"answers":{"q":{"answers":["A"]}}}`, false, true},
		{"request_user_input", "request_user_input requires non-empty options for every question", true, true},
		{"request_user_input_async", `{"accepted":true}`, false, true},
		{"request_user_input_async", "questions must not be empty", true, true},
		{"apply_patch", "Exit code: 0\nWall time: 0.1 seconds\nOutput:\nSuccess.\n", false, true},
		{"apply_patch", "apply_patch verification failed: Failed to find expected lines in /r/a.go", true, true},
		{"apply_patch", "", false, false},
		{"apply_patch", "Success. Updated the following files:\nM /r/a.go\n", false, true},
		{"apply_patch", `{"output":"Success. Updated the following files:\nM /r/a.go\n","metadata":{"exit_code":0,"duration_seconds":0.1}}`, false, true},
		{"apply_patch", `{"output":"error: patch failed","metadata":{"exit_code":1}}`, true, true},
		{"exec", "Script failed\nWall time 0.1 seconds\nOutput:\nScript error:\nboom", true, true},
		{"exec", "aborted by user after 1.0s", true, true},
		{"exec", "Script completed\nWall time 0.2 seconds\nOutput:\nok", false, true},
		{"exec_command", "Chunk ID: x\nProcess exited with code 2\nOutput:\n", true, true},
		{"update_plan", "Plan updated", false, false},
		{"request_user_input_async", "failed to parse function arguments: unknown field `question`", true, true},
	} {
		isErr, known := resultIsError(c.name, c.output)
		if isErr != c.isErr || known != c.known {
			t.Errorf("resultIsError(%q, %q) = (%v, %v), want (%v, %v)", c.name, c.output, isErr, known, c.isErr, c.known)
		}
	}
}
