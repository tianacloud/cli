package main

import (
	"errors"
	"strconv"
	"strings"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func branchCreateOptions() []cli.Flag {
	message := createMessageOption().(*cli.StringFlag)
	message.Usage = "Branch description (UTF-8, at most 2048 bytes)"
	return []cli.Flag{message,
		&cli.StringFlag{Name: "ttl", Usage: "Lifetime in seconds after creation succeeds (1..2592000); omitted means no expiry", Local: true, Validator: func(v string) error {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 1 || n > 2592000 {
				return errors.New("ttl must be an integer in 1..2592000 seconds")
			}
			return nil
		}},
		&cli.StringFlag{Name: "timestamp", Aliases: []string{"ts"}, Usage: "Fork parent data at this unsigned Unix second; omitted means latest", Local: true, Validator: func(v string) error {
			_, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return errors.New("timestamp must be an unsigned Unix second")
			}
			return nil
		}},
	}
}

func branchCreateRequest(cmd *cli.Command, name string) authclient.CreateBranchRequest {
	r := authclient.CreateBranchRequest{Name: name, Notes: cmd.String("message")}
	if cmd.IsSet("ttl") {
		v, _ := strconv.ParseInt(cmd.String("ttl"), 10, 64)
		r.TTLSeconds = &v
	}
	if cmd.IsSet("timestamp") {
		v, _ := strconv.ParseUint(cmd.String("timestamp"), 10, 64)
		r.Timestamp = &v
	}
	return r
}

func branchNameWasTrimmed(cmd *cli.Command, value string) bool {
	args := leafArguments(cmd)
	for i := 0; i < len(args); i++ {
		raw := args[i]
		if raw == "--" {
			return false
		}
		switch raw {
		case "-m", "--message", "--parent", "--ttl", "-ts", "--timestamp":
			i++
			continue
		}
		if strings.HasPrefix(raw, "-") {
			continue
		}
		if raw != value && strings.TrimSpace(raw) == value {
			return true
		}
	}
	return false
}
