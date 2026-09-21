package sqlitecli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"
)

const shellHelp = ".help\n.tables\n.schema\n.mode table|json|ndjson|csv\n.read PATH\n.quit\n"
const tablesSQL = "SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
const schemaSQL = "SELECT sql FROM sqlite_schema WHERE sql IS NOT NULL ORDER BY type, name"

func Script(ctx context.Context, c *Client, o *Output, statements []Statement) *Error {
	for i, s := range statements {
		r, e := c.Execute(ctx, s.SQL, false)
		if e == nil {
			e = o.Result(i+1, r)
		}
		if e != nil {
			e.Statement = i + 1
			return e
		}
	}
	return nil
}

// The input pump is demand-driven, so .quit never consumes another input line.
// A terminal Read itself may remain blocked on cancellation; the CLI process
// owns stdin and exits after bounded network cleanup rather than waiting on it.
func Shell(ctx context.Context, c *Client, o *Output, input io.Reader, diagnostics io.Writer, interactive bool) *Error {
	var rl *readline.Instance
	lineInput := input
	if interactive {
		in, inOK := input.(*os.File)
		out, outOK := diagnostics.(*os.File)
		if !inOK || !outOK {
			return inputError("interactive shell requires terminal streams")
		}
		var err error
		// Cancel a pending terminal read without closing the caller's stdin.
		stdin := readline.NewCancelableStdin(in)
		rl, err = readline.NewEx(&readline.Config{
			Prompt: "sqlite[tx=?]> ", HistoryLimit: 500, HistoryFile: "",
			Stdin: stdin, Stdout: out, Stderr: diagnostics,
		})
		if err != nil {
			_ = stdin.Close()
			return inputError("cannot initialize interactive line editor")
		}
		defer func() {
			_ = stdin.Close()
			_ = rl.Close()
		}()
		lineInput = &readlineInput{instance: rl}
	}
	type lineResult struct {
		text string
		err  error
	}
	requests := make(chan struct{})
	replies := make(chan lineResult)
	pumpCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		reader := bufio.NewReader(lineInput)
		for {
			select {
			case <-pumpCtx.Done():
				return
			case <-requests:
			}
			var b strings.Builder
			var err error
			for {
				var fragment []byte
				fragment, err = reader.ReadSlice('\n')
				if b.Len()+len(fragment) > MaxBytes {
					err = io.ErrShortBuffer
					break
				}
				b.Write(fragment)
				if err != bufio.ErrBufferFull {
					break
				}
			}
			select {
			case replies <- lineResult{b.String(), err}:
			case <-pumpCtx.Done():
				return
			}
		}
	}()
	buffer := ""
	index := 0
	runSQL := func(sql string) *Error {
		statements, e := Split(sql)
		if e != nil {
			return e
		}
		for _, stmt := range statements {
			index++
			r, e := c.Execute(ctx, stmt.SQL, false)
			if e == nil {
				e = o.Result(index, r)
			}
			if e != nil {
				e.Statement = index
				return e
			}
		}
		return nil
	}
	for {
		if interactive {
			prompt := "sqlite[tx=?]> "
			if buffer != "" {
				prompt = "...> "
			}
			// Readline owns both prompt and line redraw. Printing separately
			// makes its first refresh erase the prompt and look like a hang.
			rl.SetPrompt(prompt)
		}
		select {
		case <-ctx.Done():
			return interrupted(false)
		case requests <- struct{}{}:
		}
		var line lineResult
		select {
		case <-ctx.Done():
			return interrupted(false)
		case line = <-replies:
		}
		if interactive && errors.Is(line.err, readline.ErrInterrupt) {
			// Ctrl-C while editing abandons unsent input, including preceding
			// continuation lines. It does not change a database transaction.
			buffer = ""
			continue
		}
		if line.err != nil && line.err != io.EOF {
			return inputError("input unreadable or exceeds 8 MiB")
		}
		trim := strings.TrimSpace(line.text)
		var e *Error
		dotCommand := buffer == "" && strings.HasPrefix(trim, ".")
		if dotCommand {
			command, arg := trim, ""
			if i := strings.IndexAny(trim, " \t"); i >= 0 {
				command, arg = trim[:i], trim[i+1:]
			}
			arg = strings.TrimSpace(arg)
			switch command {
			case ".quit":
				if arg == "" {
					return nil
				}
				e = inputError(".quit accepts no arguments")
			case ".help":
				if arg != "" {
					e = inputError(".help accepts no arguments")
				} else if _, err := io.WriteString(diagnostics, shellHelp); err != nil {
					e = outputError()
				}
			case ".tables", ".schema":
				if arg != "" {
					e = inputError("metadata command accepts no arguments")
				} else {
					sql := tablesSQL
					if command == ".schema" {
						sql = schemaSQL
					}
					e = runSQL(sql)
				}
			case ".mode":
				e = o.SetFormat(arg)
			case ".read":
				var sql string
				sql, e = ReadScript(arg)
				if e == nil {
					e = runSQL(sql)
				}
			default:
				e = inputError("unknown shell command; use .help")
			}
		} else {
			if len(buffer)+len(line.text) > MaxBytes {
				return inputError("SQL buffer exceeds 8 MiB")
			}
			buffer += line.text
			var ready bool
			ready, e = readiness(buffer)
			if e == nil && (ready || line.err == io.EOF) {
				e = runSQL(buffer)
				buffer = ""
			}
			if e == nil && validateLexical(buffer) == nil && skipTrivia(buffer, 0) == len(buffer) {
				buffer = ""
			}
		}
		if e != nil {
			if !interactive || !dotCommand || e.ExitCode != 2 {
				return e
			}
			if _, err := fmt.Fprintln(diagnostics, e.Error()); err != nil {
				return outputError()
			}
		}
		if line.err == io.EOF {
			return nil
		}
	}
}

type readlineInput struct {
	instance *readline.Instance
	buffer   []byte
}

func (r *readlineInput) Read(p []byte) (int, error) {
	for len(r.buffer) == 0 {
		line, err := r.instance.Readline()
		if err != nil {
			return 0, err
		}
		r.buffer = append(r.buffer, line...)
		r.buffer = append(r.buffer, '\n')
	}
	n := copy(p, r.buffer)
	r.buffer = r.buffer[n:]
	return n, nil
}
