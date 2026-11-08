package store

import "testing"

func TestStripURLCredentials(t *testing.T) {
	cases := map[string][2]string{
		"https://x-access-token:ghp_secret@github.com/o/r.git": {"https://github.com/o/r.git", "ghp_secret"},
		"https://tok@github.com/o/r.git":                       {"https://github.com/o/r.git", "tok"},
		"https://github.com/o/r.git":                           {"https://github.com/o/r.git", ""},
		"git@github.com:o/r.git":                               {"git@github.com:o/r.git", ""},
		"https://github.com/o/r@v1":                            {"https://github.com/o/r@v1", ""},
	}
	for in, want := range cases {
		c, s := StripURLCredentials(in)
		if c != want[0] || s != want[1] {
			t.Errorf("%q => %q,%q want %q,%q", in, c, s, want[0], want[1])
		}
	}
}
