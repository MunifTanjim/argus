package tui

import (
	"slices"
	"testing"
)

func TestParseKeySeq(t *testing.T) {
	cases := []struct {
		in   string
		want keySeq
	}{
		{"g", keySeq{{"g"}}},
		{"G", keySeq{{"G", "shift+g"}}},
		{"g.", keySeq{{"g"}, {"."}}},
		{"<C-b>", keySeq{{"ctrl+b"}}},
		{"<c-B>", keySeq{{"ctrl+b"}}},
		{"<C-S-b>", keySeq{{"ctrl+shift+b"}}},
		{"<S-C-b>", keySeq{{"ctrl+shift+b"}}},
		{"<M-j>", keySeq{{"alt+j"}}},
		{"<A-j>", keySeq{{"alt+j"}}},
		{"<M-A>", keySeq{{"alt+A", "alt+shift+a"}}},
		{"<S-a>", keySeq{{"A", "shift+a"}}},
		{"<D-x>", keySeq{{"super+x"}}},
		{"<CR>", keySeq{{"enter"}}},
		{"<cr>", keySeq{{"enter"}}},
		{"<Enter>", keySeq{{"enter"}}},
		{"<Return>", keySeq{{"enter"}}},
		{"<Esc>", keySeq{{"esc"}}},
		{"<Space>", keySeq{{"space"}}},
		{" ", keySeq{{"space"}}},
		{"<Tab>", keySeq{{"tab"}}},
		{"<S-Tab>", keySeq{{"shift+tab"}}},
		{"<BS>", keySeq{{"backspace"}}},
		{"<Up>", keySeq{{"up"}}},
		{"<PageUp>", keySeq{{"pgup"}}},
		{"<PageDown>", keySeq{{"pgdown"}}},
		{"<Home>", keySeq{{"home"}}},
		{"<End>", keySeq{{"end"}}},
		{"<F1>", keySeq{{"f1"}}},
		{"<F12>", keySeq{{"f12"}}},
		{"<lt>", keySeq{{"<"}}},
		{"<C-]>", keySeq{{"ctrl+]"}}},
		{"g<C-b>", keySeq{{"g"}, {"ctrl+b"}}},
		{"<Leader>o", keySeq{{leaderToken}, {"o"}}},
		{"<leader>", keySeq{{leaderToken}}},
	}
	for _, c := range cases {
		got, err := parseKeySeq(c.in)
		if err != nil {
			t.Errorf("parseKeySeq(%q): %v", c.in, err)
			continue
		}
		if !slices.EqualFunc(got, c.want, slices.Equal[keyStep]) {
			t.Errorf("parseKeySeq(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseKeySeqErrors(t *testing.T) {
	for _, in := range []string{"", "<C-b", "<X-b>", "<F13>", "<foo>", "<>", "<g>", "<"} {
		if _, err := parseKeySeq(in); err == nil {
			t.Errorf("parseKeySeq(%q) should fail", in)
		}
	}
}

func TestKeySeqID(t *testing.T) {
	for in, want := range map[string]string{"<C-b>": "ctrl+b", "G": "G", "g.": seqMark + "g .", "g<C-b>": seqMark + "g ctrl+b"} {
		s, err := parseKeySeq(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.id(); got != want {
			t.Errorf("id(%q) = %q, want %q", in, got, want)
		}
	}
}
