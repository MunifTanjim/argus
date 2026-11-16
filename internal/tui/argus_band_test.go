package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/transcript"
)

const argusTestTS = "1970-01-01T00:00:00Z"

var (
	argusReply = transcript.ArgusMessage{ID: "m-1a2b3c4d", Kind: "reply", From: "worker-api", Session: "home:argus:%5",
		Body: "**Purpose**\n\n- one\n- two"}
	argusNotice = transcript.ArgusMessage{ID: "m-5e6f7a8b", Kind: "interaction", From: "argus", Child: "worker-db", ChildSession: "home:argus:%7",
		Body: "worker-db asks for permission:\nInput: {\n  \"cmd\": \"rm -rf build\"\n}"}
)

func argusEntry(msgs ...transcript.ArgusMessage) transcript.Entry {
	return transcript.Entry{ID: "u1", Kind: transcript.EntryUser, Timestamp: argusTestTS, Text: "<argus-message raw", ArgusMessages: msgs}
}

// assertGoldenText compares text, without escape sequences, to testdata/<name>.golden.
func assertGoldenText(t *testing.T, name, text string) {
	t.Helper()
	got := xansi.Strip(text)
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s: the drawn band changed\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestArgusBandGolden(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	for name, e := range map[string]transcript.Entry{
		"argus-band-reply":  argusEntry(argusReply),
		"argus-band-two":    argusEntry(argusReply, argusNotice),
		"argus-band-notice": argusEntry(argusNotice),
	} {
		band, _ := m.argusBand(e, false, 76)
		assertGoldenText(t, name, band)
		plain := xansi.Strip(band)
		if strings.Contains(plain, "You") || strings.Contains(plain, "<argus-message") {
			t.Errorf("%s: band shows the user row or the raw prompt:\n%s", name, plain)
		}
		if got, want := strings.Count(plain, "╭"), len(e.ArgusMessages); got != want {
			t.Errorf("%s: %d frames, want %d", name, got, want)
		}
		heading := ""
		for _, l := range strings.Split(plain, "\n") {
			if strings.HasPrefix(l, "│") {
				heading = l
				break
			}
		}
		if !strings.Contains(heading, "00:00:00") {
			t.Errorf("%s: no clock on the first heading:\n%s", name, plain)
		}
	}
}

func TestArgusBandHeadingAndCountLine(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	plain := xansi.Strip(must(m.argusBand(argusEntry(argusReply, argusNotice), false, 76)))
	if !strings.HasPrefix(plain, "argus · 2 messages") {
		t.Errorf("no count line first:\n%s", plain)
	}
	for _, want := range []string{"reply", "worker-api", "home:argus:%5", "m-1a2b3c4d", "prompt", "worker-db", "home:argus:%7", `  "cmd": "rm -rf build"`} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "**Purpose**") {
		t.Errorf("markdown drawn raw:\n%s", plain)
	}
}

func must(band string, _ []cardSpan) string { return band }

func TestArgusBandFoldsLongBody(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	e := argusEntry(transcript.ArgusMessage{ID: "m-1", Kind: "turn_ended", From: "argus", Body: strings.Join(lines, "\n")})
	if !m.entryExpandable(e) {
		t.Fatal("a 40-line body must be expandable")
	}
	collapsed := xansi.Strip(must(m.argusBand(e, false, 76)))
	if got := strings.Count(collapsed, "line\n") + strings.Count(collapsed, "line "); got != maxCollapsedLines {
		t.Errorf("collapsed shows %d body lines, want %d:\n%s", got, maxCollapsedLines, collapsed)
	}
	if !strings.Contains(collapsed, hiddenText(40-maxCollapsedLines)) {
		t.Errorf("no hidden-lines hint:\n%s", collapsed)
	}
	expanded := xansi.Strip(must(m.argusBand(e, true, 76)))
	if got := strings.Count(expanded, "line"); got != 40 {
		t.Errorf("expanded shows %d body lines, want 40", got)
	}
	short := argusEntry(argusReply)
	if m.entryExpandable(short) {
		t.Error("a short message must not be expandable")
	}
}

func TestUserBandWithoutArgusMessagesUnchanged(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	e := transcript.Entry{ID: "u1", Kind: transcript.EntryUser, Timestamp: argusTestTS, Text: "<argus-message id=\"m-1\" kind=\"text\">\nhi\n</argus-message>"}
	got, _ := m.entryLayout(e, false, false, false, false, 80)
	want, _ := userBand(e, false, 80-detailGutter)
	if !strings.Contains(xansi.Strip(got), "You") || !strings.Contains(got, strings.Split(want, "\n")[1]) {
		t.Errorf("entry without ArgusMessages must draw the user band:\n%s", xansi.Strip(got))
	}
}

func TestArgusBandCopySpansCopyBodies(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	band, spans := m.argusBand(argusEntry(argusReply, argusNotice), false, 76)
	var srcs []string
	total := 0
	for _, sp := range spans {
		total += sp.lines
		if sp.kind == spanText {
			srcs = append(srcs, sp.src)
		}
	}
	if len(srcs) != 2 || srcs[0] != argusReply.Body || srcs[1] != argusNotice.Body {
		t.Errorf("text spans = %q", srcs)
	}
	if total != strings.Count(band, "\n")+1 {
		t.Errorf("spans cover %d lines, band has %d", total, strings.Count(band, "\n")+1)
	}
}

func TestArgusBandSpansMatchLinesWithLongURL(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	body := "See https://example.com/" + strings.Repeat("a", 120) + "\n\n```\n" + strings.Repeat("x", 100) + "\n```"
	e := argusEntry(transcript.ArgusMessage{ID: "m-1", Kind: "reply", From: "w", Body: body})
	band, spans := m.argusBand(e, true, 76)
	total := 0
	for _, sp := range spans {
		total += sp.lines
	}
	lines := strings.Count(band, "\n") + 1
	if total != lines {
		t.Errorf("spans cover %d lines, band has %d:\n%s", total, lines, xansi.Strip(band))
	}
	if got := m.argusBodyLines(e, 76) + 3; got != lines { // top border, heading, bottom border
		t.Errorf("argusBodyLines+3 = %d, band has %d lines", got, lines)
	}
}

func TestArgusBandLineCopyLeavesOutFrame(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	e := argusEntry(argusReply, argusNotice)
	band, spans := m.argusBand(e, true, 76)
	lines := strings.Split(band, "\n")
	s := newSurface(lines, nil, nil, 0, 80, 0, len(lines))
	got := s.lineText(0, len(lines)-1, 0, len(lines), cardSource{text: e.Text, spans: func() []cardSpan { return spans }})
	for _, frame := range []string{"╭", "╰", "│"} {
		if strings.Contains(got, frame) {
			t.Errorf("copy holds frame %q:\n%s", frame, got)
		}
	}
	for _, want := range []string{"argus · 2 messages", "reply worker-api home:argus:%5 m-1a2b3c4d", "**Purpose**", `  "cmd": "rm -rf build"`} {
		if !strings.Contains(got, want) {
			t.Errorf("copy lacks %q:\n%s", want, got)
		}
	}
}

func TestArgusEntrySourceIsHeadingAndBody(t *testing.T) {
	m := bareTv()
	got, ok, _ := m.entrySource(argusEntry(argusReply))
	want := "reply worker-api home:argus:%5 m-1a2b3c4d\n" + argusReply.Body
	if !ok || got != want {
		t.Errorf("entrySource = %q, %v; want %q", got, ok, want)
	}
}

func TestArgusDetailFrameDrawsExpandedBand(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	e := argusEntry(argusReply, transcript.ArgusMessage{ID: "m-2", Kind: "turn_ended", From: "argus", Body: strings.Join(lines, "\n")})
	f := m.bodyFrame(e)
	want, wantSpans := m.argusBand(e, true, m.c.m.transcriptWidth()-detailGutter)
	if f.body != want || len(f.bodySpans) != len(wantSpans) {
		t.Errorf("detail frame is not the expanded band:\n%s", xansi.Strip(f.body))
	}
	plain := xansi.Strip(f.body)
	if strings.Contains(plain, "You") || strings.Contains(plain, "<argus-message") {
		t.Errorf("detail frame shows the user row or the raw prompt:\n%s", plain)
	}
	if strings.Count(plain, "line") != 40 {
		t.Errorf("detail frame folds the body:\n%s", plain)
	}
}

func TestArgusBandInteractionEndedLabel(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	plain := xansi.Strip(must(m.argusBand(argusEntry(transcript.ArgusMessage{ID: "m-1", Kind: "interaction_ended", From: "argus", Body: "x"}), false, 76)))
	if !strings.Contains(plain, "prompt ended") {
		t.Errorf("no prompt ended label:\n%s", plain)
	}
}

func TestArgusBandUnknownKindShowsRawKind(t *testing.T) {
	m := bareTv()
	m.c.m.width = 80
	plain := xansi.Strip(must(m.argusBand(argusEntry(transcript.ArgusMessage{ID: "m-1", Kind: "prompt_notice", From: "argus", Body: "x"}), false, 76)))
	if !strings.Contains(plain, "prompt_notice") {
		t.Errorf("no raw kind label:\n%s", plain)
	}
}
