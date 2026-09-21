package sqlitecli

import (
	"strings"
	"testing"
)

func TestSplitPreservesSource(t *testing.T) {
	for _, test := range []struct {
		input string
		want  []string
	}{
		{"SELECT 1;SELECT 2", []string{"SELECT 1;", "SELECT 2"}},
		{"-- 开始\nSELECT 'é;'; /*注释*/ SELECT '中文'; -- end", []string{"SELECT 'é;';", "SELECT '中文';"}},
		{"CREATE TRIGGER tr AFTER INSERT ON t BEGIN INSERT INTO t VALUES('a;b'); SELECT CASE WHEN 1 THEN 2 END; END; SELECT 3;", []string{"CREATE TRIGGER tr AFTER INSERT ON t BEGIN INSERT INTO t VALUES('a;b'); SELECT CASE WHEN 1 THEN 2 END; END;", "SELECT 3;"}},
		{" ; --empty\n ; /* empty */", nil},
	} {
		parts, e := Split(test.input)
		if e != nil {
			t.Fatalf("%q: %v", test.input, e)
		}
		if len(parts) != len(test.want) {
			t.Fatalf("%q: got %d parts", test.input, len(parts))
		}
		for i := range parts {
			if parts[i].SQL != test.want[i] {
				t.Errorf("part %d got %q want %q", i, parts[i].SQL, test.want[i])
			}
		}
	}
}
func TestInputPreflight(t *testing.T) {
	for _, input := range []string{"SELECT 'unfinished", "SELECT 1; SELECT FROM;", "SELECT 1\x00", "SELECT \xff;", "SELECT 1; VACUUM;", "SELECT 1; ATTACH 'x' AS x;", "SELECT 1; DETACH x;", "SELECT `a\"; COMMIT; --`;", "SELECT 1\u00a0;", "SELECT " + strings.Repeat("(", 129) + "1" + strings.Repeat(")", 129)} {
		if _, e := Split(input); e == nil {
			t.Errorf("accepted unsafe/unsupported input %q", input)
		}
	}
	for _, sql := range []string{"BEGIN", "END", "COMMIT", "ROLLBACK TO s", "SAVEPOINT s", "RELEASE s"} {
		parts, e := Split(sql)
		if e != nil || len(parts) != 1 || !parts[0].Transaction {
			t.Errorf("transaction %q: %v %v", sql, parts, e)
		}
	}
	parts, e := Split("EXPLAIN BEGIN;")
	if e != nil || parts[0].Transaction {
		t.Fatalf("EXPLAIN: %v %v", parts, e)
	}
	if _, e = Split(strings.Repeat("SELECT 1;", MaxStatements+1)); e == nil {
		t.Fatal("statement limit not enforced")
	}
}
func TestMultilineReady(t *testing.T) {
	for _, test := range []struct {
		sql   string
		ready bool
	}{
		{"SELECT ';'", false}, {"SELECT 1; --comment", true},
		{"CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1;", false},
		{"CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1; END;", true},
	} {
		if got := Ready(test.sql); got != test.ready {
			t.Errorf("%q ready=%v", test.sql, got)
		}
	}
}

func TestInvalidTerminatedSQLIsImmediate(t *testing.T) {
	if _, e := readiness("SELECT FROM;"); e == nil {
		t.Fatal("malformed terminated SQL was treated as incomplete")
	}
	if ready, e := readiness("CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1;"); ready || e != nil {
		t.Fatalf("incomplete trigger: %v %v", ready, e)
	}
}
