package authclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tianacloud/cli/internal/localfile"
	"github.com/tianacloud/sdk-go/auth"
	"os"
	"path/filepath"
	"strings"
)

// The CLI accepts only the management origin inherited from its launcher.
// Do not use SDK DefaultOrigin: older SDK releases also accept a legacy alias.
func DefaultOrigin() string { return strings.TrimSpace(os.Getenv("TIANA_API_ORIGIN")) }

type CredentialStore = auth.CredentialStore
type FileStore = auth.FileStore

var ErrCredentialNotFound = auth.ErrCredentialNotFound
var NewFileStore = auth.NewFileStore
var NewCredentialStore = auth.NewCredentialStore
var DefaultCredentialPath = auth.DefaultCredentialPath

var ErrPendingNotFound = errors.New("pending command not found")

const pendingCommandName = "pending-command.json"
const maxPendingBytes = 8 << 20

func DefaultPendingCommandPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("resolve pending command directory")
	}
	return filepath.Join(directory, "tiana", pendingCommandName), nil
}

// FilePendingCommandStore keeps a command whose control-plane effect has not
// been confirmed. It is intentionally independent of credentials.
type FilePendingCommandStore struct {
	Path string
}

func NewFilePendingCommandStore(path string) *FilePendingCommandStore {
	return &FilePendingCommandStore{Path: path}
}

func (s *FilePendingCommandStore) Load() (PendingCommand, error) {
	if s == nil || s.Path == "" {
		return PendingCommand{}, ErrPendingNotFound
	}
	contents, err := localfile.Read(s.Path, maxPendingBytes, true)
	if errors.Is(err, os.ErrNotExist) {
		return PendingCommand{}, ErrPendingNotFound
	}
	if err != nil {
		return PendingCommand{}, errors.New("cannot read pending command: require an owned, regular mode-0600 file, no symlinks, at most 8 MiB")
	}
	defer clear(contents)
	var command PendingCommand
	if err := json.Unmarshal(contents, &command); err != nil || command.Command == "" || command.IdempotencyKey == "" || command.Origin == "" {
		return PendingCommand{}, errors.New("pending command is invalid")
	}
	return command, nil
}

func (s *FilePendingCommandStore) Save(command PendingCommand) error {
	if s == nil || s.Path == "" || command.Command == "" || command.IdempotencyKey == "" || command.Origin == "" {
		return errors.New("pending command is incomplete")
	}
	contents, err := json.MarshalIndent(command, "", "  ")
	if err != nil {
		return errors.New("encode pending command")
	}
	defer clear(contents)
	if len(contents) > maxPendingBytes {
		return errors.New("pending command exceeds 8 MiB")
	}
	if err := validatePendingFile(s.Path); err != nil {
		return err
	}
	return atomicWritePrivate(s.Path, contents)
}

func (s *FilePendingCommandStore) Delete() error {
	if s == nil || s.Path == "" {
		return nil
	}
	if err := validatePendingFile(s.Path); err != nil {
		return err
	}
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove pending command: %w", err)
	}
	return nil
}

func validatePendingFile(path string) error {
	contents, err := localfile.Read(path, maxPendingBytes, true)
	clear(contents)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("unsafe pending command file; require an owned, regular mode-0600 file, no symlinks, at most 8 MiB")
	}
	return nil
}

func atomicWritePrivate(path string, contents []byte) error {
	directory := filepath.Dir(path)
	if err := preparePendingDir(directory); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".tiana-credentials-*")
	if err != nil {
		return fmt.Errorf("create private credential file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set private credential permissions: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write private credential file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync private credential file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close private credential file: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace private credential file: %w", err)
	}
	return nil // The renamed inode already has mode 0600.
}
