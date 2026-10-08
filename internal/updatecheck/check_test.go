package updatecheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestChecksPackagesIndependentlyAndSharesDailyReminder(t *testing.T) {
	now := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	counts := map[string]int{}
	var mu sync.Mutex
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"version": "2.0.0"})
	}))
	defer registry.Close()
	checker := Checker{Path: filepath.Join(t.TempDir(), "update-state.json"), Registry: registry.URL, HTTP: registry.Client(), Now: func() time.Time { return now }}
	cli := map[string]string{CLIPackage: "1.0.0"}
	both := map[string]string{CLIPackage: "1.0.0", SkillsPackage: "1.0.0"}
	first := checker.Check(t.Context(), cli, Options{Fetch: true, Notify: true})
	if first.CheckStatus != "fresh" || !first.ShouldNotify || len(first.Updates) != 1 {
		t.Fatalf("first: %+v", first)
	}
	second := checker.Check(t.Context(), both, Options{Fetch: true, Notify: true})
	if second.ShouldNotify || len(second.Updates) != 2 {
		t.Fatalf("skills: %+v", second)
	}
	cached := checker.Check(t.Context(), both, Options{Fetch: true, Notify: true})
	if cached.CheckStatus != "cached" || cached.ShouldNotify {
		t.Fatalf("cached: %+v", cached)
	}
	mu.Lock()
	if counts["/@tianacloud%2Fcli/latest"]+counts["/@tianacloud/cli/latest"] != 1 || counts["/@tianacloud%2Fagent-skills/latest"]+counts["/@tianacloud/agent-skills/latest"] != 1 {
		t.Fatalf("counts: %+v", counts)
	}
	mu.Unlock()
	now = now.Add(24 * time.Hour)
	again := checker.Check(t.Context(), both, Options{Fetch: true, Notify: true})
	if !again.ShouldNotify {
		t.Fatalf("next day: %+v", again)
	}
	upgraded := checker.Check(t.Context(), map[string]string{CLIPackage: "2.0.0", SkillsPackage: "3.0.0"}, Options{Notify: true})
	if upgraded.ShouldNotify || len(upgraded.Updates) != 0 {
		t.Fatalf("installed versions: %+v", upgraded)
	}
}

func TestFailureRecordsAttemptAndDoesNotClaimLatest(t *testing.T) {
	calls := 0
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
	defer registry.Close()
	checker := Checker{Path: filepath.Join(t.TempDir(), "state.json"), Registry: registry.URL, HTTP: registry.Client()}
	for i := 0; i < 2; i++ {
		result := checker.Check(t.Context(), map[string]string{CLIPackage: "1.0.0"}, Options{Fetch: true, Notify: true})
		if result.CheckStatus != "unavailable" || result.ShouldNotify {
			t.Fatalf("failure: %+v", result)
		}
	}
	if calls != 1 {
		t.Fatalf("failed attempts: %d", calls)
	}
	checker.Path = filepath.Join(t.TempDir(), "file", "state.json")
	os.WriteFile(filepath.Dir(checker.Path), []byte("not a directory"), 0600)
	if result := checker.Check(t.Context(), map[string]string{CLIPackage: "1.0.0"}, Options{Fetch: true, Notify: true}); result.ShouldNotify || result.CheckStatus != "unavailable" {
		t.Fatalf("unwritable state: %+v", result)
	}
}

func TestSemVerOrdering(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{{"1.0.0", "1.0.1", true}, {"2.0.0", "1.9.9", false}, {"1.0.0-beta.9", "1.0.0-beta.10", true}, {"1.0.0-beta.10", "1.0.0", true}, {"1.0.0+build.1", "1.0.0+build.2", false}, {"dev", "1.0.0", false}} {
		if got := newer(tc.current, tc.latest); got != tc.want {
			t.Errorf("%s → %s: %t", tc.current, tc.latest, got)
		}
	}
}

func TestParallelProcessesReserveOnlyOneReminder(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "state.json")
	seed := State{Packages: map[string]PackageState{CLIPackage: {LastCheckAttemptAt: now, LastCheckSuccessAt: now, LatestVersion: "2.0.0"}}}
	data, _ := json.Marshal(seed)
	os.WriteFile(path, data, 0600)
	var commands []*exec.Cmd
	for i := 0; i < 8; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestUpdateCheckProcessHelper$")
		cmd.Env = append(os.Environ(), "TIANA_UPDATE_TEST_FILE="+path)
		commands = append(commands, cmd)
	}
	var wg sync.WaitGroup
	results := make(chan Result, 8)
	failures := make(chan error, 8)
	for _, cmd := range commands {
		wg.Add(1)
		go func(cmd *exec.Cmd) {
			defer wg.Done()
			out, err := cmd.Output()
			if err != nil {
				failures <- err
				return
			}
			var result Result
			if err = json.Unmarshal(out, &result); err != nil {
				failures <- err
				return
			}
			results <- result
		}(cmd)
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	notifications := 0
	for result := range results {
		if result.ShouldNotify {
			notifications++
		}
	}
	if notifications != 1 {
		t.Fatalf("notifications: %d", notifications)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err = json.Unmarshal(saved, &state); err != nil || state.LastNotifiedAt.IsZero() {
		t.Fatalf("state: %s %v", saved, err)
	}
}
func TestUpdateCheckProcessHelper(t *testing.T) {
	path := os.Getenv("TIANA_UPDATE_TEST_FILE")
	if path == "" {
		return
	}
	result := (Checker{Path: path}).Check(t.Context(), map[string]string{CLIPackage: "1.0.0"}, Options{Notify: true})
	json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}

func TestRefreshDoesNotConsumeReminderAndExplicitCheckTimesOut(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer registry.Close()
	checker := Checker{Path: filepath.Join(t.TempDir(), "state.json"), Registry: registry.URL, HTTP: registry.Client()}
	start := time.Now()
	result := checker.Check(t.Context(), map[string]string{CLIPackage: "1.0.0", SkillsPackage: "1.0.0"}, Options{Fetch: true})
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
		t.Fatalf("check waited %s", elapsed)
	}
	if result.ShouldNotify || result.CheckStatus != "unavailable" {
		t.Fatalf("timeout: %+v", result)
	}
	saved, _ := os.ReadFile(checker.Path)
	var state State
	json.Unmarshal(saved, &state)
	if !state.LastNotifiedAt.IsZero() {
		t.Fatal("background refresh consumed reminder")
	}
}

func TestSuccessfulBackgroundRefreshLeavesReminderForNextCall(t *testing.T) {
	var calls int
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]string{"version": "2.0.0"})
	}))
	defer registry.Close()
	checker := Checker{Path: filepath.Join(t.TempDir(), "state.json"), Registry: registry.URL, HTTP: registry.Client()}
	background := checker.Check(t.Context(), map[string]string{CLIPackage: "1.0.0"}, Options{Fetch: true})
	if background.ShouldNotify || len(background.Updates) != 1 {
		t.Fatalf("refresh: %+v", background)
	}
	start := time.Now()
	cached := checker.Check(t.Context(), map[string]string{CLIPackage: "1.0.0"}, Options{Notify: true})
	if !cached.ShouldNotify || calls != 1 || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("cache: %+v, requests %d", cached, calls)
	}
}

func TestLockIsReleasedWhenHolderProcessDies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	child := exec.Command(os.Args[0], "-test.run=^TestUpdateLockProcessHelper$")
	child.Env = append(os.Environ(), "TIANA_UPDATE_LOCK_TEST_FILE="+path)
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	ready := make([]byte, 1)
	if _, err = output.Read(ready); err != nil || ready[0] != 'R' {
		t.Fatalf("holder: %q %v", ready, err)
	}
	checker := Checker{Path: path}
	if result := checker.Check(t.Context(), map[string]string{CLIPackage: "1.0.0"}, Options{}); result.CheckStatus != "skipped" {
		t.Fatalf("while held: %+v", result)
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if result := checker.Check(t.Context(), map[string]string{CLIPackage: "1.0.0"}, Options{}); result.CheckStatus == "skipped" {
		t.Fatalf("after exit: %+v", result)
	}
}
func TestUpdateLockProcessHelper(t *testing.T) {
	path := os.Getenv("TIANA_UPDATE_LOCK_TEST_FILE")
	if path == "" {
		return
	}
	release, err := tryLock(t.Context(), path+".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	os.Stdout.Write([]byte("R"))
	time.Sleep(time.Minute)
}
