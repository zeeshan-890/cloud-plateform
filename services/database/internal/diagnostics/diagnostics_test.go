package diagnostics

import (
	"strings"
	"testing"
)

func TestRedactQuery(t *testing.T) {
	raw := "SELECT id, email FROM users WHERE id = 42 AND email = 'alice@example.com' AND secret = 'my-super-secret-key'"
	redacted := RedactQuery(raw)
	for _, leak := range []string{"my-super-secret-key", "alice@example.com", "42"} {
		if strings.Contains(redacted, leak) {
			t.Errorf("%q leaked in redacted query: %s", leak, redacted)
		}
	}
}

func TestGenerateFingerprint(t *testing.T) {
	q1 := "SELECT * FROM orders WHERE user_id = 100 AND status = 'pending'"
	q2 := "SELECT   *   FROM orders WHERE user_id = 9999   AND status = 'shipped'"
	if fp1, fp2 := GenerateFingerprint(q1), GenerateFingerprint(q2); fp1 != fp2 {
		t.Errorf("expected fingerprints to match:\nfp1: %s\nfp2: %s", fp1, fp2)
	}
}

func TestParseSlowQueriesFromPsqlOutput(t *testing.T) {
	out := "120\t5400.25\t45.00\t120\tSELECT * FROM orders WHERE user_id = 7 AND token = 'abc'\n" +
		"3\t30.50\t10.17\t3\tUPDATE users SET password = 'hunter2' WHERE id = 5\n"
	qs, err := ParseSlowQueries(out)
	if err != nil || len(qs) != 2 {
		t.Fatalf("err=%v qs=%v", err, qs)
	}
	if qs[0].Calls != 120 || qs[0].TotalTimeMs != 5400.25 || qs[0].MeanTimeMs != 45 || qs[0].RowsExamined != 120 {
		t.Fatalf("%+v", qs[0])
	}
	for _, q := range qs {
		if strings.Contains(q.Query, "hunter2") || strings.Contains(q.Query, "abc") || strings.Contains(q.Fingerprint, "hunter2") {
			t.Fatalf("literals must be redacted: %+v", q)
		}
	}
}

func TestParseSlowQueriesEmptyAndMalformed(t *testing.T) {
	if qs, err := ParseSlowQueries("\n"); err != nil || qs == nil || len(qs) != 0 {
		t.Fatalf("empty output must yield an empty (non-nil) list: %v %v", qs, err)
	}
	for _, bad := range []string{"only one column", "x\t1\t1\t1\tq", "1\t1\t1\n"} {
		if _, err := ParseSlowQueries(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestParseConnections(t *testing.T) {
	a, m, err := ParseConnections("7\t100\n")
	if err != nil || a != 7 || m != 100 {
		t.Fatal(a, m, err)
	}
	if _, _, err := ParseConnections("garbage"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestQueriesTargetPgStatStatements(t *testing.T) {
	if !strings.Contains(SlowQueriesSQL, "pg_stat_statements") || !strings.Contains(SlowQueriesSQL, "mean_exec_time") || !strings.Contains(EnableExtensionSQL, "pg_stat_statements") {
		t.Fatal("diagnostics must read pg_stat_statements")
	}
}
