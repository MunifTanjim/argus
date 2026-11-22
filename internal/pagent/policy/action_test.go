package policy

import (
	"encoding/json"
	"reflect"
	"testing"
)

func bashIn(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func TestDescribeShellTarget(t *testing.T) {
	tests := []struct{ cmd, target string }{
		{"git status", "git"},
		{"FOO=1 BAR=2 git status", "git"},
		{"env -i FOO=1 gh pr list", "gh"},
		{"env -u HOME git log", "git"},
		{"sudo -u root git status", "git"},
		{"sudo env A=b nohup time command git x", "git"},
		{"/usr/bin/git log", "git"},
		{"cd x && git push", "cd"},
		{"cat a | grep b", "cat"},
		{"echo 'a && b' ; ls", "echo"},
		{`bash -c "git push origin"`, "git"},
		{"", ""},
	}
	for _, tt := range tests {
		a := Describe("claude", "Bash", bashIn(tt.cmd))
		if a.Target != tt.target {
			t.Errorf("%q: target %q, want %q", tt.cmd, a.Target, tt.target)
		}
	}
}

func TestDescribeTools(t *testing.T) {
	tests := []struct {
		name   string
		tool   string
		in     string
		target string
	}{
		{"edit", "Edit", `{"file_path":"/a/b.go"}`, "/a/b.go"},
		{"write", "Write", `{"file_path":"x.md"}`, "x.md"},
		{"multiedit", "MultiEdit", `{"file_path":"y.md","edits":[]}`, "y.md"},
		{"notebook", "NotebookEdit", `{"notebook_path":"n.ipynb"}`, "n.ipynb"},
		{"webfetch", "WebFetch", `{"url":"https://Example.com:8080/x?y=1","prompt":"p"}`, "example.com"},
		{"websearch", "WebSearch", `{"query":"q"}`, ""},
		{"mcp", "mcp__gmail__list_threads", `{"q":"x"}`, "list_threads"},
		{"codex exec", "exec_command", `{"cmd":"git status","workdir":"/w"}`, "git"},
		{"codex argv", "exec", `{"command":["bash","-lc","gh pr list"]}`, "gh"},
		{"codex patch json string", "apply_patch", `"*** Begin Patch\n*** Update File: a/b.go\n@@\n-x\n+y\n*** End Patch"`, "a/b.go"},
		{"codex patch raw", "apply_patch", "*** Begin Patch\n*** Add File: n.txt\n+x\n*** End Patch", "n.txt"},
		{"pi bash", "bash", `{"command":"rsync -a x y"}`, "rsync"},
		{"pi edit", "edit", `{"path":"p.txt"}`, "p.txt"},
		{"pi write", "write", `{"filePath":"q.txt"}`, "q.txt"},
		{"opencode no input", "webfetch", ``, ""},
		{"unknown", "Frobnicate", `{"a":1}`, ""},
	}
	for _, tt := range tests {
		a := Describe("x", tt.tool, json.RawMessage(tt.in))
		if a.Tool != tt.tool || a.Target != tt.target {
			t.Errorf("%s: got tool=%q target=%q, want target %q", tt.name, a.Tool, a.Target, tt.target)
		}
	}
}

func TestClassifyShell(t *testing.T) {
	out, irr, sec := []string{ClassOutward}, []string{ClassIrreversible}, []string{ClassSecurity}
	tests := []struct {
		cmd  string
		want []string
	}{
		{"git push", out},
		{"git push origin main", out},
		{"git -C repo push", out},
		{"cd x && git push", out},
		{"git status; git push", out},
		{"git push --force", []string{ClassOutward, ClassIrreversible}},
		{"git push -f origin", []string{ClassOutward, ClassIrreversible}},
		{"git push origin +main", []string{ClassOutward, ClassIrreversible}},
		{"git push --force-with-lease", []string{ClassOutward, ClassIrreversible}},
		{"git status", nil},
		{"git log --oneline", nil},
		{"git commit -m 'git push'", nil},
		{"gh pr create --fill", out},
		{"gh pr comment 1 -b x", out},
		{"gh pr merge 1", out},
		{"gh issue create", out},
		{"gh issue comment 2", out},
		{"gh -R a/b pr create", out},
		{"gh pr list", nil},
		{"gh issue view 3", nil},
		{"curl https://x.io", nil},
		{"curl -sL https://x.io", nil},
		{"curl -H 'X-A: b' https://x.io", nil},
		{"curl -X POST https://x.io", out},
		{"curl -XPUT https://x.io", out},
		{"curl --request=DELETE https://x.io", out},
		{"curl -X GET https://x.io", nil},
		{"curl -d a=b https://x.io", out},
		{"curl --data-binary @f https://x.io", out},
		{"curl -sd a=b https://x.io", out},
		{"curl -F f=@x https://x.io", out},
		{"curl -T f https://x.io", out},
		{"wget https://x.io", nil},
		{"wget --post-data=a https://x.io", out},
		{"wget --method=PUT https://x.io", out},
		{"ssh host ls", out},
		{"scp a host:b", out},
		{"scp a user@host:b", out},
		{"scp a b", nil},
		{"rsync -a src/ host:dst", out},
		{"rsync -a src/ dst/", nil},
		{"rm -rf /tmp/x", irr},
		{"rm -fr x", irr},
		{"rm -r -f x", irr},
		{"rm --recursive --force x", irr},
		{"sudo rm -rf x", irr},
		{"rm file", nil},
		{"rm -r dir", nil},
		{"git reset --hard HEAD~1", irr},
		{"git reset HEAD file", nil},
		{"git clean -fd", irr},
		{"chmod 700 ~/.ssh", sec},
		{"chmod -R 755 $HOME/bin", sec},
		{"chown me /tmp/x", nil},
		{"ssh-keygen -t ed25519", sec},
		{"gpg --import k", sec},
		{"git config --global user.name x", sec},
		{"git config user.name x", nil},
		{"FOO=1 sudo git push", out},
		{"bash -c 'ls && git push'", out},
		{"ls", nil},
	}
	for _, tt := range tests {
		got := Describe("claude", "Bash", bashIn(tt.cmd)).Class
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q: class %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestClassifyOtherTools(t *testing.T) {
	sec := []string{ClassSecurity}
	out := []string{ClassOutward}
	irr := []string{ClassIrreversible}
	tests := []struct {
		name string
		tool string
		in   string
		want []string
	}{
		{"edit ordinary", "Edit", `{"file_path":"src/a.go"}`, nil},
		{"edit env", "Edit", `{"file_path":"/p/.env"}`, sec},
		{"write env.local", "Write", `{"file_path":"/p/.env.local"}`, sec},
		{"write env.example", "Write", `{"file_path":"/p/.env.example"}`, nil},
		{"write environment.go", "Write", `{"file_path":"/p/environment.go"}`, nil},
		{"edit ssh", "Edit", `{"file_path":"/home/u/.ssh/config"}`, sec},
		{"edit gnupg", "Edit", `{"file_path":"/home/u/.gnupg/gpg.conf"}`, sec},
		{"notebook", "NotebookEdit", `{"notebook_path":"n.ipynb"}`, nil},
		{"patch env", "apply_patch", "*** Begin Patch\n*** Update File: a.go\n*** Add File: .env\n+x\n*** End Patch", sec},
		{"patch ordinary", "apply_patch", "*** Begin Patch\n*** Update File: a.go\n*** End Patch", nil},
		{"webfetch get", "WebFetch", `{"url":"https://a.io"}`, nil},
		{"webfetch post", "WebFetch", `{"url":"https://a.io","method":"POST"}`, out},
		{"webfetch body", "WebFetch", `{"url":"https://a.io","body":"x"}`, out},
		{"websearch", "WebSearch", `{"query":"q"}`, nil},
		{"mcp read", "mcp__gmail__list_threads", `{}`, nil},
		{"mcp send", "mcp__gmail__send_email", `{}`, out},
		{"mcp post", "mcp__slack__post_message", `{}`, out},
		{"mcp reply", "mcp__x__reply", `{}`, out},
		{"mcp publish", "mcp__x__publishPage", `{}`, out},
		{"mcp comment", "mcp__x__add_comment", `{}`, out},
		{"mcp share", "mcp__x__share_doc", `{}`, out},
		{"mcp invite", "mcp__x__invite_user", `{}`, out},
		{"mcp delete", "mcp__x__delete_file", `{}`, irr},
		{"mcp destroy", "mcp__x__destroy", `{}`, irr},
		{"mcp drop", "mcp__x__drop_table", `{}`, irr},
		{"mcp purge", "mcp__x__purge", `{}`, irr},
		{"mcp archive", "mcp__x__archive_thread", `{}`, irr},
		{"mcp hint", "mcp__x__do_thing", `{"destructiveHint":true}`, irr},
		{"mcp both", "mcp__x__send_and_delete", `{}`, []string{ClassOutward, ClassIrreversible}},
		{"server name not matched", "mcp__sender__list", `{}`, nil},
		{"keyword inside a word", "mcp__db__postgres_query", `{}`, nil},
		{"keyword inside a camel word", "mcp__x__getPostgresRows", `{}`, nil},
		{"keyword prefix of a word", "mcp__x__sendgrid-status", `{}`, nil},
		{"dash segment", "mcp__x__send-mail", `{}`, out},
		{"unknown tool", "Frobnicate", `{"command":"git push"}`, nil},
	}
	for _, tt := range tests {
		got := Describe("claude", tt.tool, json.RawMessage(tt.in)).Class
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: class %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestClassifyUsesInput(t *testing.T) {
	a := Action{Tool: "Bash", Input: bashIn("git push")}
	if got := Classify(a); !reflect.DeepEqual(got, []string{ClassOutward}) {
		t.Errorf("got %v", got)
	}
}

func TestShellLine(t *testing.T) {
	for _, c := range []struct {
		tool, in, want string
		ok             bool
	}{
		{"Bash", `{"command":"git push origin main"}`, "git push origin main", true},
		{"exec_command", `{"cmd":"ls -la","workdir":"/tmp"}`, "ls -la", true},
		{"shell", `{"command":["bash","-lc","echo 'hi there'"]}`, `bash -lc 'echo '\''hi there'\'''`, true},
		{"Bash", `{"description":"x"}`, "", false},
		{"Edit", `{"file_path":"a"}`, "", false},
	} {
		got, ok := Action{Tool: c.tool, Input: json.RawMessage(c.in)}.ShellLine()
		if got != c.want || ok != c.ok {
			t.Errorf("%s %s: got %q %v, want %q %v", c.tool, c.in, got, ok, c.want, c.ok)
		}
	}
}
