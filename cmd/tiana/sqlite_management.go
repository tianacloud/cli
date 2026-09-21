package main

import (
	"fmt"

	"github.com/tianacloud/cli/internal/authclient"
)

// A scope shares management/idempotency machinery without sharing engine
// authorization. The zero value is used only by generic pagination helpers.
type databaseScope struct{ engine string }

var sqliteManagementScope = databaseScope{engine: "sqlite"}

func (s databaseScope) check(instance authclient.Instance) error {
	if s.engine != "" && instance.Engine != s.engine {
		return fmt.Errorf("instance engine must be %s", s.engine)
	}
	return nil
}

func (s databaseScope) filter(instances []authclient.Instance) []authclient.Instance {
	if s.engine == "" {
		return instances
	}
	var matches []authclient.Instance
	for _, instance := range instances {
		if instance.Engine == s.engine {
			matches = append(matches, instance)
		}
	}
	return matches
}
