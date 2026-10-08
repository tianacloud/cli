package sqlitecli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

type Output struct {
	writer            io.Writer
	format            string
	started, finished bool
	csvColumns        []string
}

func ValidFormat(format string) bool {
	return format == "table" || format == "json" || format == "ndjson" || format == "csv"
}
func NewOutput(w io.Writer, format string) *Output { return &Output{writer: w, format: format} }
func (o *Output) SetFormat(format string) *Error {
	if !ValidFormat(format) {
		return inputError("mode must be table, json, ndjson or csv")
	}
	if e := o.Finish(); e != nil {
		return e
	}
	o.format = format
	o.started = false
	o.finished = false
	o.csvColumns = nil
	return nil
}
func (o *Output) write(s string) *Error {
	n, err := io.WriteString(o.writer, s)
	if err != nil || n != len(s) {
		return outputError()
	}
	return nil
}
func (o *Output) json(v any) *Error {
	b, err := json.Marshal(v)
	if err != nil {
		return outputError()
	}
	return o.write(string(b))
}
func (o *Output) Result(index int, r *Result) *Error {
	if !r.Valid() {
		return unknown("invalid result")
	}
	affected := strconv.FormatUint(*r.Affected, 10)
	switch o.format {
	case "json":
		prefix := "[\n"
		if o.started {
			prefix = ",\n"
		}
		if e := o.write(prefix); e != nil {
			return e
		}
		if e := o.json(struct {
			Statement int       `json:"statement"`
			Columns   []Column  `json:"columns"`
			Rows      [][]Value `json:"rows"`
			Affected  string    `json:"affected_rows"`
			RowID     *string   `json:"last_insert_rowid"`
		}{index, r.Columns, r.Rows, affected, r.LastInsertRowID}); e != nil {
			return e
		}
	case "ndjson":
		if e := o.line(map[string]any{"type": "columns", "statement": index, "columns": r.Columns}); e != nil {
			return e
		}
		for _, row := range r.Rows {
			if e := o.line(map[string]any{"type": "row", "statement": index, "values": row}); e != nil {
				return e
			}
		}
		if e := o.line(map[string]any{"type": "statement_end", "statement": index, "affected_rows": affected, "last_insert_rowid": r.LastInsertRowID}); e != nil {
			return e
		}
	case "table":
		if len(r.Rows) > 1000 {
			return failure("TABLE_LIMIT", "table display is limited to 1000 rows; select json, ndjson or csv", 6)
		}
		if len(r.Columns) > 0 {
			names := columnNames(r.Columns)
			for i := range names {
				names[i] = visible(names[i])
			}
			if e := o.write(strings.Join(names, " | ") + "\n"); e != nil {
				return e
			}
			for _, row := range r.Rows {
				values := make([]string, len(row))
				for i, v := range row {
					values[i] = visible(displayValue(v))
				}
				if e := o.write(strings.Join(values, " | ") + "\n"); e != nil {
					return e
				}
			}
		}
		if e := o.write(fmt.Sprintf("(%d rows, %s affected)\n", len(r.Rows), affected)); e != nil {
			return e
		}
	case "csv":
		if len(r.Columns) > 0 {
			names := columnNames(r.Columns)
			if o.csvColumns == nil {
				if e := o.csv(names); e != nil {
					return e
				}
				o.csvColumns = names
			} else {
				if len(names) != len(o.csvColumns) {
					return failure("CSV_SCHEMA_CHANGED", "CSV requires identical columns; output is partial", 6)
				}
				for i := range names {
					if names[i] != o.csvColumns[i] {
						return failure("CSV_SCHEMA_CHANGED", "CSV requires identical columns; output is partial", 6)
					}
				}
			}
			for _, row := range r.Rows {
				values := make([]string, len(row))
				for i, v := range row {
					values[i] = displayValue(v)
				}
				if e := o.csv(values); e != nil {
					return e
				}
			}
		}
	default:
		return inputError("invalid output format")
	}
	o.started = true
	return nil
}
func (o *Output) line(v any) *Error {
	if e := o.json(v); e != nil {
		return e
	}
	return o.write("\n")
}
func (o *Output) csv(values []string) *Error {
	for i, v := range values {
		if i > 0 {
			if e := o.write(","); e != nil {
				return e
			}
		}
		if e := o.write("\"" + strings.ReplaceAll(v, "\"", "\"\"") + "\""); e != nil {
			return e
		}
	}
	return o.write("\r\n")
}
func (o *Output) Finish() *Error {
	if o.finished {
		return nil
	}
	o.finished = true
	if o.format == "json" {
		if o.started {
			return o.write("\n]\n")
		}
		return o.write("[]\n")
	}
	return nil
}
func columnNames(columns []Column) []string {
	names := make([]string, len(columns))
	for i, c := range columns {
		if c.Name != nil {
			names[i] = *c.Name
		}
	}
	return names
}
func displayValue(v Value) string {
	if v.Type == "null" {
		return "NULL"
	}
	if v.Type == "blob" {
		return "base64:" + *v.Base64
	}
	if v.Type == "text" || v.Type == "integer" {
		var s string
		_ = json.Unmarshal(v.Value, &s)
		return s
	}
	return string(v.Value)
}
func visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			fmt.Fprintf(&b, "\\u{%x}", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
