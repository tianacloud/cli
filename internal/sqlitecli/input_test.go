package sqlitecli

import (
	"encoding/json"
	"os"
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

func TestPinnedVectorSyntaxPreflight(t *testing.T) {
	data, err := os.ReadFile("testdata/sqlite-vec-syntax.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, SQL string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			parts, e := Split(test.SQL)
			if e != nil || len(parts) != 1 || parts[0].SQL != test.SQL {
				t.Fatalf("parts=%+v error=%v", parts, e)
			}
		})
	}
}
func TestInputPreflight(t *testing.T) {
	for _, input := range []string{"SELECT 'unfinished", "SELECT 1\x00", "SELECT \xff;", "SELECT 1; VACUUM;", "SELECT 1; ATTACH 'x' AS x;", "SELECT 1; DETACH x;", "SELECT " + strings.Repeat("(", 129) + "1" + strings.Repeat(")", 129)} {
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

func TestInvalidTerminatedSQLIsDelegated(t *testing.T) {
	if ready, e := readiness("SELECT FROM;"); !ready || e != nil {
		t.Fatalf("terminated grammar error must reach server: ready=%v error=%v", ready, e)
	}
	if ready, e := readiness("CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1;"); ready || e != nil {
		t.Fatalf("incomplete trigger: %v %v", ready, e)
	}
}

func TestVirtualTableModuleArgumentsPreserveSource(t *testing.T) {
	for _, sql := range []string{
		"CREATE VIRTUAL TABLE note_vectors USING vec0(embedding float[2]);",
		"CREATE VIRTUAL TABLE IF NOT EXISTS temp.v USING vec0(id text primary key, embedding int8[2], tenant integer partition key, +contents text, chunk_size=8);",
		"CREATE VIRTUAL TABLE v USING vec0(embedding float(2) distance_metric L2, chunk_size 8);",
		"CREATE VIRTUAL TABLE \"向量\" USING vec0(embedding float[2], +label text /* ); COMMIT; */);",
		"CREATE VIRTUAL TABLE docs USING fts5(body, tokenize='unicode61 remove_diacritics 2', prefix='2 3');",
		"CREATE VIRTUAL TABLE boxes USING rtree(id,minX,maxX,+label);",
		"CREATE VIRTUAL TABLE docs USING fts5([a'b]);",
		"CREATE VIRTUAL TABLE docs USING fts5([a--b]);",
		"CREATE VIRTUAL TABLE rowid USING vec0(embedding float[2]);",
		"CREATE VIRTUAL TABLE v USING custom([a); COMMIT;\"'`], nested(foo(2)), 'a);b''c');",
		"EXPLAIN QUERY PLAN CREATE VIRTUAL TABLE v USING vec0(embedding float[2]);",
	} {
		parts, e := Split(sql)
		if e != nil || len(parts) != 1 || parts[0].SQL != sql || parts[0].Transaction {
			t.Fatalf("%q: parts=%+v error=%v", sql, parts, e)
		}
		if ready, e := readiness(sql); !ready || e != nil {
			t.Fatalf("%q: ready=%v error=%v", sql, ready, e)
		}
	}
}

// These tokens are legal in SQLite's tokenizer even when an installed module
// rejects their meaning. The CLI must forward them unchanged to that module.
func TestVirtualTableNativeTokensPreserveModuleGrammar(t *testing.T) {
	for _, args := range []string{
		".5, 2., 1e-2, 0xFF, 1_000, 0xF_F",
		"x'', X'01aB', 'x''y', [a!#b]",
		"a != b, a <> b, a ->> b, a << b, a || b",
		"?123, :named, @named, $ns::name(suffix), #named",
		"向量 float[2], foo(bar(2)), +label text",
	} {
		sql := "CREATE VIRTUAL TABLE v USING custom(" + args + ");"
		parts, e := Split(sql)
		if e != nil || len(parts) != 1 || parts[0].SQL != sql {
			t.Fatalf("legal native tokens %q: parts=%+v error=%v", args, parts, e)
		}
	}
	for _, args := range []string{
		"x'0'", "X'0g'", "0x", "0xFG", "12e", "12e+", "1é",
		"$", "#", ":", "$::", "$name(unterminated space)", "a } b", "a \x01 b",
	} {
		if _, e := Split("INSERT INTO t VALUES(1); CREATE VIRTUAL TABLE v USING custom(" + args + ");"); e == nil {
			t.Errorf("accepted native illegal token %q", args)
		}
	}
}

func TestVirtualTableArgumentsKeepStatementAndTransactionBoundaries(t *testing.T) {
	ddl := "CREATE VIRTUAL TABLE v USING custom('); COMMIT;', [); BEGIN;], nested(2));"
	parts, e := Split(ddl + " /* comment */ BEGIN; SELECT 1; COMMIT;")
	if e != nil || len(parts) != 4 {
		t.Fatalf("parts=%+v error=%v", parts, e)
	}
	for i, want := range []Statement{{SQL: ddl}, {SQL: "BEGIN;", Transaction: true}, {SQL: "SELECT 1;"}, {SQL: "COMMIT;", Transaction: true}} {
		if parts[i] != want {
			t.Errorf("part %d: got %+v want %+v", i, parts[i], want)
		}
	}
}

func TestVirtualTableLexicalAndPolicyFailuresPreflight(t *testing.T) {
	for _, sql := range []string{
		"CREATE VIRTUAL TABLE v USING vec0(embedding float[2]); ATTACH 'x' AS x;",
		"INSERT INTO t VALUES(1); CREATE VIRTUAL TABLE v USING fts5(a # b);",
		"INSERT INTO t VALUES(1); CREATE VIRTUAL TABLE v USING fts5(a ! b);",
		"INSERT INTO t VALUES(1); CREATE VIRTUAL TABLE v USING fts5(a ] b);",
		"INSERT INTO t VALUES(1); CREATE VIRTUAL TABLE v USING fts5(a { b);",
		"INSERT INTO t VALUES(1); CREATE VIRTUAL TABLE v USING fts5(a \\ b);",
		"INSERT INTO t VALUES(1); CREATE VIRTUAL TABLE v USING fts5(x'gg');",
		"INSERT INTO t VALUES(1); CREATE VIRTUAL TABLE v USING fts5(1abc);",
		"INSERT INTO t VALUES(1); CREATE VIRTUAL TABLE v USING fts5(@);",
	} {
		if _, e := Split(sql); e == nil {
			t.Errorf("accepted malformed/unsupported outer syntax %q", sql)
		}
	}
	if ready, e := readiness("CREATE VIRTUAL TABLE v USING vec0(embedding float[2],\n"); ready || e != nil {
		t.Fatalf("unfinished argument list: ready=%v error=%v", ready, e)
	}
}

func TestRowIDIdentifiersPreserveSourceAndWithoutRowID(t *testing.T) {
	for _, sql := range []string{
		"INSERT INTO note_vectors(rowid,embedding) VALUES(1,'[1,0]');",
		"-- 中文\nINSERT INTO \"向量\"(RoWiD,embedding) VALUES(1,'rowid');",
		"CREATE TABLE t(rowid INTEGER PRIMARY KEY, label TEXT) WITHOUT /* comment */ ROWID;",
		"SELECT rowid, 'rowid', \"rowid\" FROM t;",
	} {
		parts, e := Split(sql)
		if e != nil || len(parts) != 1 || parts[0].Transaction || parts[0].SQL != sql[skipTrivia(sql, 0):] {
			t.Fatalf("%q: parts=%+v error=%v", sql, parts, e)
		}
	}

}

func TestRowIDAfterOpaqueBracketArgument(t *testing.T) {
	sql := "CREATE VIRTUAL TABLE v USING custom([a'\"`]); INSERT INTO v(rowid) VALUES(1);"
	parts, e := Split(sql)
	if e != nil || len(parts) != 2 || parts[1].SQL != "INSERT INTO v(rowid) VALUES(1);" {
		t.Fatalf("parts=%+v error=%v", parts, e)
	}
}

// Removing the grammar gate must send legal native syntax without rewriting it,
// and leave terminated grammar errors for the authoritative server parser.
func TestNativeGrammarIsDelegatedToServer(t *testing.T) {
	for _, sql := range []string{
		"SELECT median(value ORDER BY value DESC) FROM generate_series(1,3);",
		"SELECT json_group_array(value ORDER BY value DESC) FROM generate_series(1,3);",
		"SELECT [text_reverse]('abc');",
		"SELECT json_array(1_000);",
		"SELECT 1_000 AS value;",
		"SELECT `a\"; COMMIT; --`;",
		"SELECT \"a`; BEGIN; --\";",
		"SELECT :ns::var(a'b);",
		"SELECT FROM;",
		"insert inot t values(1);",
	} {
		parts, e := Split(sql)
		if e != nil || len(parts) != 1 || parts[0].SQL != sql || parts[0].Transaction {
			t.Fatalf("%q parts=%+v error=%v", sql, parts, e)
		}
		if ready, e := readiness(sql); !ready || e != nil {
			t.Fatalf("%q ready=%v error=%v", sql, ready, e)
		}
	}
}

func TestLexicalSplitKeepsTriggersAndModuleSemicolons(t *testing.T) {
	for _, sql := range []string{
		"CREATE VIRTUAL TABLE v USING custom(a; COMMIT; b);",
		"EXPLAIN QUERY PLAN CREATE TEMP TRIGGER tr AFTER INSERT ON t BEGIN SELECT CASE WHEN 1 THEN 'END;' ELSE 'BEGIN;' END; UPDATE t SET x=1; END;",
		"CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT CASE WHEN 1 THEN CASE WHEN 1 THEN 2 END ELSE 3 END; END;",
		"CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1; END",
		"CREATE TABLE [a'b; COMMIT;](x);",
	} {
		parts, e := Split(sql + "\nSELECT 2;")
		if !strings.HasSuffix(sql, ";") {
			parts, e = Split(sql)
		}
		want := 2
		if !strings.HasSuffix(sql, ";") {
			want = 1
		}
		if e != nil || len(parts) != want || parts[0].SQL != sql || parts[0].Transaction {
			t.Fatalf("boundary %q parts=%+v error=%v", sql, parts, e)
		}
	}
}

func TestExplicitUnsupportedCommandPolicies(t *testing.T) {
	for _, sql := range []string{
		"ATTACH '' AS aux;", "/*comment*/ DETACH DATABASE aux;", "VACUUM;", "VACUUM INTO 'x';",
		"EXPLAIN ATTACH '' AS aux;", "EXPLAIN QUERY PLAN VACUUM;",
		"SELECT 1; /*comment*/ ATTACH ':memory:' AS aux;",
	} {
		if _, e := Split(sql); e == nil {
			t.Errorf("policy accepted %q", sql)
		}
	}
	for _, sql := range []string{"SELECT 'ATTACH';", "SELECT [VACUUM] FROM t;", "CREATE TABLE detach(x);"} {
		if _, e := Split(sql); e != nil {
			t.Errorf("policy interpreted data as a command: %q %v", sql, e)
		}
	}
}

func TestEmptyDelimitersDoNotHideCommandPolicies(t *testing.T) {
	for _, sql := range []string{"; ATTACH '' AS aux;", ";; /*x*/ VACUUM;", "\xef\xbb\xbf; DETACH aux;"} {
		if _, e := Split(sql); e == nil {
			t.Errorf("leading empty delimiter hid command %q", sql)
		}
	}
	for _, sql := range []string{"; BEGIN;", "\xef\xbb\xbf;; /*x*/ END;", "; SAVEPOINT s;"} {
		parts, e := Split(sql)
		if e != nil || len(parts) != 1 || !parts[0].Transaction {
			t.Errorf("lost transaction control %q: %+v %v", sql, parts, e)
		}
	}
}
