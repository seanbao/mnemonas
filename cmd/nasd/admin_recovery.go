package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/seanbao/mnemonas/internal/auth"
)

func recoverAdminOnly(configPath, username string, output io.Writer) (returnErr error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("administrator username cannot be empty")
	}
	if output == nil {
		return errors.New("administrator recovery output is required")
	}
	if err := auth.CheckAdminRecoverySupported(); err != nil {
		return err
	}

	cfg, _, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	if !cfg.Auth.Enabled {
		return errors.New("administrator recovery requires auth.enabled=true")
	}
	if err := requireExistingRecoveryUsersFile(cfg.Auth.UsersFile); err != nil {
		return err
	}

	stateLock, err := auth.AcquireStateLock(cfg.Auth.UsersFile)
	if err != nil {
		return err
	}
	defer func() {
		if err := stateLock.Close(); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()

	result, recoveryErr := stateLock.RecoverAdminPassword(cfg.Auth.UsersFile, username)
	if recoveryErr != nil && (!auth.IsPersistenceWarning(recoveryErr) || result == nil) {
		return recoveryErr
	}
	if result == nil {
		return errors.New("administrator recovery returned no result")
	}
	if result.Username == "" || result.CredentialPath == "" {
		return errors.New("administrator recovery returned incomplete metadata")
	}

	status := "created"
	if result.AlreadyAvailable {
		status = "already_available"
	} else if result.Resumed {
		status = "resumed"
	}

	var summary strings.Builder
	summary.WriteString("administrator recovery completed\n")
	_, _ = fmt.Fprintf(&summary, "username: %q\n", result.Username)
	_, _ = fmt.Fprintf(&summary, "credential_file: %q\n", result.CredentialPath)
	_, _ = fmt.Fprintf(&summary, "status: %s\n", status)
	if recoveryErr != nil {
		summary.WriteString("warning: recovery state was committed, but storage durability could not be fully confirmed\n")
	}
	if _, err := io.WriteString(output, summary.String()); err != nil {
		return fmt.Errorf("write administrator recovery summary: %w", err)
	}
	return nil
}

func requireExistingRecoveryUsersFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("administrator recovery users file does not exist: %q", path)
		}
		return fmt.Errorf("inspect administrator recovery users file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("administrator recovery users file must be a regular file: %q", path)
	}
	return nil
}
