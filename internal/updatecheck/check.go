// Package updatecheck shares package checks and the reminder allowance across CLI and Skills.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tianacloud/cli/internal/localfile"
	"github.com/tianacloud/cli/internal/localstate"
	"golang.org/x/mod/semver"
)

const (
	CLIPackage    = "@tianacloud/cli"
	SkillsPackage = "@tianacloud/agent-skills"
	interval      = 24 * time.Hour
)

var errBusy = errors.New("update state is in use")

type PackageState struct {
	LastCheckAttemptAt time.Time `json:"last_check_attempt_at"`
	LastCheckSuccessAt time.Time `json:"last_check_success_at"`
	LatestVersion      string    `json:"latest_version"`
}
type State struct {
	Packages       map[string]PackageState `json:"packages"`
	LastNotifiedAt time.Time               `json:"last_notified_at"`
}
type Update struct {
	Package        string `json:"package"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
}
type Result struct {
	ShouldNotify    bool     `json:"should_notify"`
	CheckStatus     string   `json:"check_status"`
	Updates         []Update `json:"updates"`
	UnknownVersions []string `json:"unknown_versions,omitempty"`
	RefreshDue      bool     `json:"-"`
}
type Options struct{ Fetch, Notify bool }
type Checker struct {
	Path, Registry string
	HTTP           *http.Client
	Now            func() time.Time
}

func DefaultPath() (string, error) {
	root, err := localstate.Directory()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "update-state.json"), nil
}
func (c Checker) Check(ctx context.Context, versions map[string]string, options Options) Result {
	result := Result{CheckStatus: "unavailable", Updates: []Update{}}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	if c.Path == "" {
		var err error
		c.Path, err = DefaultPath()
		if err != nil {
			return result
		}
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0700); err != nil {
		return result
	}
	release, err := tryLock(ctx, c.Path+".lock")
	if err != nil {
		if errors.Is(err, errBusy) {
			result.CheckStatus = "skipped"
		}
		return result
	}
	defer release()
	state := State{Packages: map[string]PackageState{}}
	data, err := os.ReadFile(c.Path)
	if err == nil {
		if json.Unmarshal(data, &state) != nil {
			return result
		}
		if state.Packages == nil {
			state.Packages = map[string]PackageState{}
		}
	} else if !os.IsNotExist(err) {
		return result
	}
	names := []string{}
	due := []string{}
	for _, name := range []string{CLIPackage, SkillsPackage} {
		if _, ok := versions[name]; ok {
			names = append(names, name)
			last := state.Packages[name].LastCheckAttemptAt
			if last.IsZero() || now.Sub(last) >= interval {
				due = append(due, name)
			}
		}
	}
	result.RefreshDue = len(due) > 0
	queried := options.Fetch && len(due) > 0
	if queried {
		for _, name := range due {
			item := state.Packages[name]
			item.LastCheckAttemptAt = now
			state.Packages[name] = item
		}
		if writeState(c.Path, state) != nil {
			return result
		}
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		type answer struct {
			name, version string
			err           error
		}
		answers := make(chan answer, len(due))
		var wait sync.WaitGroup
		for _, name := range due {
			wait.Add(1)
			go func(name string) {
				defer wait.Done()
				version, err := c.latest(checkCtx, name)
				answers <- answer{name, version, err}
			}(name)
		}
		wait.Wait()
		cancel()
		close(answers)
		for answer := range answers {
			if answer.err == nil {
				item := state.Packages[answer.name]
				item.LatestVersion = answer.version
				item.LastCheckSuccessAt = now
				state.Packages[answer.name] = item
			}
		}
		if writeState(c.Path, state) != nil {
			return result
		}
		result.RefreshDue = false
	}
	result.CheckStatus = "cached"
	if queried {
		result.CheckStatus = "fresh"
	}
	for _, name := range names {
		item := state.Packages[name]
		if item.LastCheckSuccessAt.IsZero() || item.LastCheckSuccessAt.Before(item.LastCheckAttemptAt) {
			result.CheckStatus = "unavailable"
		}
		if !semver.IsValid("v" + versions[name]) {
			result.UnknownVersions = append(result.UnknownVersions, name)
		}
		if newer(versions[name], item.LatestVersion) {
			result.Updates = append(result.Updates, Update{Package: name, CurrentVersion: versions[name], LatestVersion: item.LatestVersion})
		}
	}
	if options.Notify && len(result.Updates) > 0 && (state.LastNotifiedAt.IsZero() || now.Sub(state.LastNotifiedAt) >= interval) {
		state.LastNotifiedAt = now
		if writeState(c.Path, state) == nil {
			result.ShouldNotify = true
		}
	}
	return result
}
func (c Checker) latest(ctx context.Context, name string) (string, error) {
	registry := c.Registry
	if registry == "" {
		registry = "https://registry.npmjs.org"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(registry, "/")+"/"+url.PathEscape(name)+"/latest", nil)
	if err != nil {
		return "", err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("registry check unavailable")
	}
	var descriptor struct {
		Version string `json:"version"`
	}
	if err = json.NewDecoder(response.Body).Decode(&descriptor); err != nil {
		return "", err
	}
	if !semver.IsValid("v" + descriptor.Version) {
		return "", errors.New("registry version is unavailable")
	}
	return descriptor.Version, nil
}
func newer(current, latest string) bool {
	return semver.IsValid("v"+current) && semver.IsValid("v"+latest) && semver.Compare("v"+latest, "v"+current) > 0
}
func writeState(path string, state State) error {
	file, err := localfile.CreateTemp(filepath.Dir(path), ".update-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = json.NewEncoder(file).Encode(state); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func Message(updates []Update) string {
	parts := make([]string, 0, len(updates))
	for _, update := range updates {
		parts = append(parts, update.Package+" "+update.CurrentVersion+" → "+update.LatestVersion)
	}
	sort.Strings(parts)
	return "Updates available: " + strings.Join(parts, ", ") + ". Confirm before upgrading."
}
