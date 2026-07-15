//go:build unix

package storage

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func TestAtomicWriteRenameProbeJournalRejectsSpecialManifestFilesWithoutBlocking(t *testing.T) {
	tests := []struct {
		name    string
		replace func(t *testing.T, path string)
	}{
		{
			name: "symlink",
			replace: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
					t.Fatalf("write symlink target: %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("create manifest symlink: %v", err)
				}
			},
		},
		{
			name: "fifo",
			replace: func(t *testing.T, path string) {
				t.Helper()
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatalf("create manifest FIFO: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()

			interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, "namespace:peer_in_files_first")
			manifestPath := filepath.Join(
				internalRoot.Name(),
				atomicWriteRenameProbeJournalDir,
				atomicWriteRenameProbeManifestName,
			)
			if err := os.Remove(manifestPath); err != nil {
				t.Fatalf("remove owned manifest: %v", err)
			}
			test.replace(t, manifestPath)

			err := probeAtomicWriteRenames(filesRoot, internalRoot)
			if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
				t.Fatalf("probe with special manifest error = %v, want ErrWriteAtomicRenameUnsupported", err)
			}
			info, statErr := os.Lstat(manifestPath)
			if statErr != nil {
				t.Fatalf("special manifest was removed: %v", statErr)
			}
			if test.name == "symlink" && info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("preserved manifest mode = %v, want symlink", info.Mode())
			}
			if test.name == "fifo" && info.Mode()&os.ModeNamedPipe == 0 {
				t.Fatalf("preserved manifest mode = %v, want FIFO", info.Mode())
			}
		})
	}
}

func TestAtomicWriteRenameProbeJournalRecoversAfterSIGKILL(t *testing.T) {
	modes := []string{"sigkill", "sigkill-pending"}
	for _, role := range []string{"source", "peer"} {
		for _, stage := range []string{
			"after_create",
			"after_partial_write",
			"after_file_sync",
			"after_publish",
		} {
			modes = append(modes, "sigkill-object-"+role+"-"+stage)
		}
	}
	for _, mode := range modes {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()

			runAtomicWriteRenameProbeSubprocess(
				t,
				mode,
				filesRoot.Name(),
				internalRoot.Name(),
				false,
			)
			if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
				t.Fatalf("recover probe after %s: %v", mode, err)
			}
			assertNoAtomicWriteRenameProbeResidue(t, filesRoot, internalRoot)
			assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
		})
	}
}

func TestAtomicWriteRenameProbeJournalUsesCrossProcessLock(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()

	ctx, err := openAtomicWriteRenameProbeJournalContext(
		filesRoot,
		internalRoot,
		rootio.RenameLeafBetweenRootsNoReplace,
		rootio.ExchangeLeavesBetweenRoots,
	)
	if err != nil {
		t.Fatalf("open locked probe journal context: %v", err)
	}
	runAtomicWriteRenameProbeSubprocess(
		t,
		"expect-lock",
		filesRoot.Name(),
		internalRoot.Name(),
		true,
	)
	if err := ctx.close(); err != nil {
		t.Fatalf("close locked probe journal context: %v", err)
	}
	if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
		t.Fatalf("probe after cross-process lock release: %v", err)
	}
}

func TestAtomicWriteRenameProbeJournalSubprocess(t *testing.T) {
	mode := os.Getenv("MNEMONAS_PROBE_SUBPROCESS_MODE")
	if mode == "" {
		return
	}
	filesRoot, err := os.OpenRoot(os.Getenv("MNEMONAS_PROBE_FILES_ROOT"))
	if err != nil {
		t.Fatalf("open subprocess files root: %v", err)
	}
	defer filesRoot.Close()
	internalRoot, err := os.OpenRoot(os.Getenv("MNEMONAS_PROBE_INTERNAL_ROOT"))
	if err != nil {
		t.Fatalf("open subprocess internal root: %v", err)
	}
	defer internalRoot.Close()

	switch mode {
	case "sigkill", "sigkill-pending":
		atomicWriteRenameProbeFaultHook = func(point string) error {
			target := "namespace:exchange_forward"
			if mode == "sigkill-pending" {
				target = "namespace:journal_pending_partial:setup.json"
			}
			if point == target {
				_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
				select {}
			}
			return nil
		}
		if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
			t.Fatalf("subprocess probe before SIGKILL: %v", err)
		}
		t.Fatal("subprocess probe returned without SIGKILL")
	case "expect-lock":
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
			!errors.Is(err, errAtomicWriteRenameProbeJournalLocked) {
			t.Fatalf("subprocess lock error = %v, want locked journal", err)
		}
	default:
		const prefix = "sigkill-object-"
		if !strings.HasPrefix(mode, prefix) {
			t.Fatalf("unknown probe subprocess mode %q", mode)
		}
		spec := strings.TrimPrefix(mode, prefix)
		role := ""
		for _, candidate := range []string{"source", "peer"} {
			if strings.HasPrefix(spec, candidate+"-") {
				role = candidate
				spec = strings.TrimPrefix(spec, candidate+"-")
				break
			}
		}
		if role == "" {
			t.Fatalf("invalid probe object SIGKILL mode %q", mode)
		}
		target := atomicWriteRenameProbeObjectFaultPoint(role, spec)
		atomicWriteRenameProbeFaultHook = func(point string) error {
			if point == target {
				_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
				select {}
			}
			return nil
		}
		if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
			t.Fatalf("subprocess probe before object SIGKILL: %v", err)
		}
		t.Fatal("subprocess probe returned without object SIGKILL")
	}
}

func TestAtomicWriteRenameProbeJournalRejectsSpecialPendingProbeObjectsWithoutBlocking(t *testing.T) {
	tests := []struct {
		name    string
		replace func(t *testing.T, path string)
	}{
		{
			name: "symlink",
			replace: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("pending probe object"), 0o600); err != nil {
					t.Fatalf("write pending-object symlink target: %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("create pending-object symlink: %v", err)
				}
			},
		},
		{
			name: "fifo",
			replace: func(t *testing.T, path string) {
				t.Helper()
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatalf("create pending-object FIFO: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()

			interruptAtomicWriteRenameProbeAt(
				t,
				filesRoot,
				internalRoot,
				atomicWriteRenameProbeObjectFaultPoint("source", "after_partial_write"),
			)
			setup := readAtomicWriteRenameProbeSetupForTest(t, internalRoot)
			pendingRel, _ := atomicWriteRenameProbeObjectPathsForTest(setup, "source")
			pendingPath := filepath.Join(internalRoot.Name(), pendingRel)
			if err := os.Remove(pendingPath); err != nil {
				t.Fatalf("remove owned pending probe object: %v", err)
			}
			test.replace(t, pendingPath)

			err := probeAtomicWriteRenames(filesRoot, internalRoot)
			if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
				!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
				t.Fatalf("probe with special pending object error = %v, want ownership failure", err)
			}
			info, statErr := os.Lstat(pendingPath)
			if statErr != nil {
				t.Fatalf("special pending probe object was removed: %v", statErr)
			}
			if test.name == "symlink" && info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("preserved pending-object mode = %v, want symlink", info.Mode())
			}
			if test.name == "fifo" && info.Mode()&os.ModeNamedPipe == 0 {
				t.Fatalf("preserved pending-object mode = %v, want FIFO", info.Mode())
			}
		})
	}
}

func TestAtomicWriteRenameProbeJournalRejectsSpecialPendingRecordsWithoutBlocking(t *testing.T) {
	tests := []struct {
		name    string
		replace func(t *testing.T, path string)
	}{
		{
			name: "symlink",
			replace: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
					t.Fatalf("write pending symlink target: %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("create pending symlink: %v", err)
				}
			},
		},
		{
			name: "fifo",
			replace: func(t *testing.T, path string) {
				t.Helper()
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatalf("create pending FIFO: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()
			if err := rootio.MkdirNoFollow(internalRoot, atomicWriteRenameProbeJournalDir, 0o700); err != nil {
				t.Fatalf("create probe journal: %v", err)
			}
			pendingPath := filepath.Join(
				internalRoot.Name(),
				atomicWriteRenameProbeJournalDir,
				atomicWriteRenameProbePendingName(atomicWriteRenameProbeSetupName),
			)
			test.replace(t, pendingPath)

			err := probeAtomicWriteRenames(filesRoot, internalRoot)
			if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
				t.Fatalf("probe with special pending record error = %v, want fail closed", err)
			}
			if _, statErr := os.Lstat(pendingPath); statErr != nil {
				t.Fatalf("special pending record was removed: %v", statErr)
			}
		})
	}
}

func runAtomicWriteRenameProbeSubprocess(
	t *testing.T,
	mode string,
	filesRoot string,
	internalRoot string,
	wantSuccess bool,
) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate storage test executable: %v", err)
	}
	command := exec.Command(executable, "-test.run=^TestAtomicWriteRenameProbeJournalSubprocess$")
	command.Env = append(
		os.Environ(),
		"MNEMONAS_PROBE_SUBPROCESS_MODE="+mode,
		"MNEMONAS_PROBE_FILES_ROOT="+filesRoot,
		"MNEMONAS_PROBE_INTERNAL_ROOT="+internalRoot,
	)
	output, err := command.CombinedOutput()
	if wantSuccess {
		if err != nil {
			t.Fatalf("probe subprocess %s error: %v\n%s", mode, err, output)
		}
		return
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("probe subprocess %s error = %v, want SIGKILL\n%s", mode, err, output)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("probe subprocess %s status = %v, want SIGKILL\n%s", mode, exitErr.Sys(), output)
	}
}
