package sqlitecli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func typedResult(t *testing.T) *Result {
	t.Helper()
	var result Result
	err := json.Unmarshal([]byte(`{"cols":[{"name":"x","decltype":null},{"name":"x","decltype":"TEXT"},{"name":"b","decltype":null},{"name":"n","decltype":null}],"rows":[[{"type":"integer","value":"9223372036854775807"},{"type":"text","value":""},{"type":"blob","base64":"AP8"},{"type":"null"}]],"affected_row_count":0,"last_insert_rowid":null}`), &result)
	if err != nil {
		t.Fatal(err)
	}
	return &result
}
func TestTypedOutput(t *testing.T) {
	for _, format := range []string{"json", "ndjson", "csv", "table"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			o := NewOutput(&out, format)
			if e := o.Result(1, typedResult(t)); e != nil {
				t.Fatal(e)
			}
			if e := o.Finish(); e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(out.String(), "9223372036854775807") {
				t.Fatal("lost integer precision")
			}
			switch format {
			case "json":
				var results []map[string]json.RawMessage
				if json.Unmarshal(out.Bytes(), &results) != nil || len(results) != 1 {
					t.Fatal(out.String())
				}
				if string(results[0]["affected_rows"]) != `"0"` {
					t.Fatal("row count not string")
				}
			case "ndjson":
				if len(strings.Split(strings.TrimSpace(out.String()), "\n")) != 3 {
					t.Fatal(out.String())
				}
			case "csv":
				if !strings.HasPrefix(out.String(), "\"x\",\"x\",\"b\",\"n\"\r\n") || !strings.Contains(out.String(), "\"base64:AP8\",\"NULL\"") {
					t.Fatal(out.String())
				}
			}
		})
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestOutputSafety(t *testing.T) {
	r := typedResult(t)
	if e := NewOutput(brokenWriter{}, "json").Result(1, r); e == nil || e.ExitCode != 6 {
		t.Fatal(e)
	}
	var out bytes.Buffer
	o := NewOutput(&out, "csv")
	if e := o.Result(1, r); e != nil {
		t.Fatal(e)
	}
	name := "changed"
	r.Columns[0].Name = &name
	if e := o.Result(2, r); e == nil || e.Code != "CSV_SCHEMA_CHANGED" {
		t.Fatal(e)
	}
	if strings.ContainsAny(visible("x\x1b[31m\u202ey"), "\x1b\u202e") {
		t.Fatal("terminal controls unescaped")
	}
	r.Rows = make([][]Value, 1001)
	for i := range r.Rows {
		r.Rows[i] = []Value{{Type: "null"}, {Type: "null"}, {Type: "null"}, {Type: "null"}}
	}
	if e := NewOutput(io.Discard, "table").Result(1, r); e == nil || e.Code != "TABLE_LIMIT" {
		t.Fatal(e)
	}
}
