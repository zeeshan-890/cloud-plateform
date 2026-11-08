package cron

import (
	"strings"
	"testing"
	"time"
)

func ts(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNext(t *testing.T) {
	cases := []struct{ expr, from, want string }{
		{"@hourly", "2024-03-10 10:15", "2024-03-10 11:00"},
		{"@daily", "2024-03-10 10:15", "2024-03-11 00:00"},
		{"*/15 * * * *", "2024-03-10 10:15", "2024-03-10 10:30"},
		{"30 2 * * *", "2024-03-10 02:30", "2024-03-11 02:30"},
		{"0 9 * * 1-5", "2024-03-09 12:00", "2024-03-11 09:00"}, // Saturday -> Monday
		{"0 0 1 * *", "2024-01-31 23:59", "2024-02-01 00:00"},
		{"0 0 29 2 *", "2023-03-01 00:00", "2024-02-29 00:00"},
		{"5,35 8-9 * * *", "2024-03-10 08:35", "2024-03-10 09:05"},
		{"0 0 * * 7", "2024-03-10 00:00", "2024-03-17 00:00"}, // 7 == Sunday
		{"@every 90m", "2024-03-10 10:00", "2024-03-10 11:30"},
		{"0 12 15 * 1", "2024-03-01 00:00", "2024-03-04 12:00"}, // dom OR dow
	}
	for _, c := range cases {
		s, err := Parse(c.expr)
		if err != nil {
			t.Fatalf("%q: %v", c.expr, err)
		}
		if got := s.Next(ts(c.from)); !got.Equal(ts(c.want)) {
			t.Errorf("%q from %s = %s, want %s", c.expr, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, e := range []string{"", "garbage", "* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "*/0 * * * *", "5-1 * * * *",
		"@every 1s", "@every 9999h", "@every nope", "a b c d e", strings.Repeat("1 ", 60)} {
		if _, err := Parse(e); err == nil {
			t.Errorf("%q should be rejected", e)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := &Schedule{Name: "nightly job", Cron: "0 3 * * *", Command: []string{"node", "x.js"}}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Schedule{
		{Name: "", Cron: "@daily"}, {Name: "<script>", Cron: "@daily"}, {Name: "x", Cron: "bad"},
		{Name: "x", Cron: "@daily", TimeoutSeconds: 999999}, {Name: "x", Cron: "@daily", Command: []string{"a\x00b"}},
		{Name: "x", Cron: "@daily", Command: make([]string, 40)},
	} {
		if err := Validate(s); err == nil {
			t.Errorf("%+v should be invalid", s)
		}
	}
}
