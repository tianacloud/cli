//go:build linux

package supervisor

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
)

func newChildIdentity(pid int) (ChildIdentity, error) {
	if pid <= 0 {
		return ChildIdentity{}, ErrHelperProtocol
	}
	first, err := procStartTime(pid)
	if err != nil {
		return ChildIdentity{}, err
	}
	second, err := procStartTime(pid)
	if err != nil || first != second {
		return ChildIdentity{}, fmt.Errorf("child identity changed")
	}
	return ChildIdentity{PID: pid, StartTime: first}, nil
}

func procStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, fmt.Errorf("read child identity: %w", err)
	}
	return parseProcStartTime(data)
}

func parseProcStartTime(data []byte) (uint64, error) {
	closeParen := bytes.LastIndexByte(data, ')')
	if closeParen < 0 || closeParen+2 > len(data) {
		return 0, ErrHelperProtocol
	}
	fields := bytes.Fields(data[closeParen+2:])
	// The slice starts at stat field 3; starttime is field 22.
	if len(fields) <= 19 {
		return 0, ErrHelperProtocol
	}
	value, err := strconv.ParseUint(string(fields[19]), 10, 64)
	if err != nil {
		return 0, ErrHelperProtocol
	}
	return value, nil
}
