package storage

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/rootio"
)

func TestAtomicWriteRenameProbeJournalRecoversEveryDurableBoundary(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	var points []string
	seen := make(map[string]bool)
	atomicWriteRenameProbeFaultHook = func(point string) error {
		if !seen[point] {
			seen[point] = true
			points = append(points, point)
		}
		return nil
	}
	if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
		t.Fatalf("collect durable probe boundaries: %v", err)
	}
	atomicWriteRenameProbeFaultHook = func(string) error { return nil }
	closeRoots()

	if len(points) < len(atomicWriteRenameProbePhases) {
		t.Fatalf("durable boundary count = %d, want at least %d", len(points), len(atomicWriteRenameProbePhases))
	}
	for _, point := range points {
		point := point
		t.Run(strings.NewReplacer(":", "_", "/", "_").Replace(point), func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()

			interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, point)
			atomicWriteRenameProbeFaultHook = func(string) error { return nil }
			if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
				t.Fatalf("recover and rerun probe after %s: %v", point, err)
			}
			assertNoAtomicWriteRenameProbeResidue(t, filesRoot, internalRoot)
			assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
		})
	}
	atomicWriteRenameProbeFaultHook = func(string) error { return nil }
}

func TestAtomicWriteRenameProbeJournalRecoveryIsIdempotent(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()

	interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, "namespace:exchange_forward")
	interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, "checkpoint:recovery_source_isolated")
	interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, "checkpoint:recovery_peer_removed")

	atomicWriteRenameProbeFaultHook = func(string) error { return nil }
	if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
		t.Fatalf("finish repeated recovery and rerun probe: %v", err)
	}
	assertNoAtomicWriteRenameProbeResidue(t, filesRoot, internalRoot)
	assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
}

func TestAtomicWriteRenameProbeJournalRejectsCorruptRecordsAndPreservesExactFilesSlot(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, internalRoot *os.Root, manifestRel string, original []byte)
	}{
		{
			name: "truncated manifest",
			mutate: func(t *testing.T, internalRoot *os.Root, manifestRel string, _ []byte) {
				t.Helper()
				replaceAtomicWriteRenameProbeJournalFile(t, internalRoot, manifestRel, []byte("{"))
			},
		},
		{
			name: "unknown manifest field",
			mutate: func(t *testing.T, internalRoot *os.Root, manifestRel string, original []byte) {
				t.Helper()
				trimmed := strings.TrimSpace(string(original))
				trimmed = strings.TrimSuffix(trimmed, "}") + `,"unknown":true}` + "\n"
				replaceAtomicWriteRenameProbeJournalFile(t, internalRoot, manifestRel, []byte(trimmed))
			},
		},
		{
			name: "unknown journal entry",
			mutate: func(t *testing.T, internalRoot *os.Root, _ string, _ []byte) {
				t.Helper()
				unknownRel := filepath.Join(atomicWriteRenameProbeJournalDir, "unexpected")
				file, err := rootio.OpenFileNoFollow(
					internalRoot,
					unknownRel,
					os.O_WRONLY|os.O_CREATE|os.O_EXCL,
					0o600,
				)
				if err != nil {
					t.Fatalf("create unknown journal entry: %v", err)
				}
				if err := file.Close(); err != nil {
					t.Fatalf("close unknown journal entry: %v", err)
				}
			},
		},
		{
			name: "nested pending object name",
			mutate: func(t *testing.T, internalRoot *os.Root, _ string, _ []byte) {
				t.Helper()
				unknownRel := filepath.Join(
					atomicWriteRenameProbeJournalDir,
					atomicWriteRenameProbePendingName(
						atomicWriteRenameProbePendingName(
							atomicWriteRenameProbeSourceIsolation,
						),
					),
				)
				if err := internalRoot.WriteFile(unknownRel, []byte("unknown"), 0o600); err != nil {
					t.Fatalf("create nested pending object entry: %v", err)
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
			manifestRel := filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeManifestName)
			original, err := internalRoot.ReadFile(manifestRel)
			if err != nil {
				t.Fatalf("read probe manifest: %v", err)
			}
			var manifest atomicWriteRenameProbeJournalManifest
			if err := json.Unmarshal(original, &manifest); err != nil {
				t.Fatalf("decode original probe manifest: %v", err)
			}
			test.mutate(t, internalRoot, manifestRel, original)

			atomicWriteRenameProbeFaultHook = func(string) error { return nil }
			err = probeAtomicWriteRenames(filesRoot, internalRoot)
			if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
				t.Fatalf("probe with corrupt journal error = %v, want ErrWriteAtomicRenameUnsupported", err)
			}
			if _, err := filesRoot.Lstat(manifest.Paths.FilesSlot); err != nil {
				t.Fatalf("exact files slot was removed after corrupt journal: %v", err)
			}
		})
	}
}

func TestAtomicWriteRenameProbeJournalRejectsObjectReplacementWithoutDeletingIt(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()

	interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, "namespace:peer_in_files_first")
	manifest := readAtomicWriteRenameProbeManifestForTest(t, internalRoot)
	if err := filesRoot.Remove(manifest.Paths.FilesSlot); err != nil {
		t.Fatalf("remove owned files slot before replacement: %v", err)
	}
	replacement, err := rootio.OpenFileNoFollow(
		filesRoot,
		manifest.Paths.FilesSlot,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		t.Fatalf("create files-slot replacement: %v", err)
	}
	if _, err := replacement.Write([]byte("user-owned replacement")); err != nil {
		t.Fatalf("write files-slot replacement: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatalf("close files-slot replacement: %v", err)
	}

	atomicWriteRenameProbeFaultHook = func(string) error { return nil }
	err = probeAtomicWriteRenames(filesRoot, internalRoot)
	if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
		!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
		t.Fatalf("probe with replaced files slot error = %v, want fail-closed ownership error", err)
	}
	data, readErr := filesRoot.ReadFile(manifest.Paths.FilesSlot)
	if readErr != nil {
		t.Fatalf("read preserved files-slot replacement: %v", readErr)
	}
	if got, want := string(data), "user-owned replacement"; got != want {
		t.Fatalf("preserved files-slot replacement = %q, want %q", got, want)
	}
}

func TestAtomicWriteRenameProbeJournalRecoversSetupAndRejectsUnknownSetupObject(t *testing.T) {
	t.Run("setup crash recovers", func(t *testing.T) {
		for _, point := range []string{
			"before:setup_intent",
			"before:journal_pending:setup.json",
			"namespace:journal_pending_created:setup.json",
			"namespace:journal_pending_partial:setup.json",
			"checkpoint:journal_pending:setup.json",
			"checkpoint:setup_intent",
			"namespace:setup_source_created",
			"checkpoint:setup_source_bound",
			"namespace:setup_peer_created",
			"checkpoint:setup_peer_bound",
			"checkpoint:manifest_persisted",
		} {
			point := point
			t.Run(strings.NewReplacer(":", "_").Replace(point), func(t *testing.T) {
				filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
				defer closeRoots()
				interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, point)
				if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
					t.Fatalf("recover setup interruption at %s: %v", point, err)
				}
				assertNoAtomicWriteRenameProbeResidue(t, filesRoot, internalRoot)
				assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
			})
		}
	})

	t.Run("incomplete setup cleanup crash recovers", func(t *testing.T) {
		for _, point := range []string{
			"checkpoint:incomplete_setup_peer_binding_removed",
			"checkpoint:incomplete_setup_peer_object_removed",
			"checkpoint:incomplete_setup_source_binding_removed",
			"checkpoint:incomplete_setup_source_object_removed",
			"checkpoint:incomplete_setup_intent_removed",
		} {
			point := point
			t.Run(strings.NewReplacer(":", "_").Replace(point), func(t *testing.T) {
				filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
				defer closeRoots()

				interruptAtomicWriteRenameProbeAt(
					t,
					filesRoot,
					internalRoot,
					"checkpoint:setup_peer_bound",
				)
				interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, point)
				if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
					t.Fatalf("recover incomplete setup cleanup interruption at %s: %v", point, err)
				}
				assertNoAtomicWriteRenameProbeResidue(t, filesRoot, internalRoot)
				assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
			})
		}
	})

	t.Run("truncated setup intent fails closed", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, "checkpoint:setup_intent")
		setupRel := filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSetupName)
		replaceAtomicWriteRenameProbeJournalFile(t, internalRoot, setupRel, []byte("{"))
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
			t.Fatalf("probe with truncated setup intent error = %v, want ErrWriteAtomicRenameUnsupported", err)
		}
	})

	t.Run("empty and partial pending records are discarded", func(t *testing.T) {
		t.Run("empty setup pending", func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()
			if err := rootio.MkdirNoFollow(internalRoot, atomicWriteRenameProbeJournalDir, 0o700); err != nil {
				t.Fatalf("create probe journal: %v", err)
			}
			pendingRel := filepath.Join(
				atomicWriteRenameProbeJournalDir,
				atomicWriteRenameProbePendingName(atomicWriteRenameProbeSetupName),
			)
			file, err := rootio.OpenFileNoFollow(
				internalRoot,
				pendingRel,
				os.O_WRONLY|os.O_CREATE|os.O_EXCL,
				0o600,
			)
			if err != nil {
				t.Fatalf("create empty setup pending record: %v", err)
			}
			if err := file.Close(); err != nil {
				t.Fatalf("close empty setup pending record: %v", err)
			}
			if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
				t.Fatalf("recover empty setup pending record: %v", err)
			}
			assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
		})

		t.Run("partial setup pending", func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()
			interruptAtomicWriteRenameProbeAt(
				t,
				filesRoot,
				internalRoot,
				"checkpoint:journal_pending:setup.json",
			)
			pendingRel := filepath.Join(
				atomicWriteRenameProbeJournalDir,
				atomicWriteRenameProbePendingName(atomicWriteRenameProbeSetupName),
			)
			replaceAtomicWriteRenameProbeJournalFile(t, internalRoot, pendingRel, []byte("{"))
			if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
				t.Fatalf("recover partial setup pending record: %v", err)
			}
			assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
		})

		t.Run("partial after-phase pending", func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()
			targetName := atomicWriteRenameProbePhaseName(16)
			interruptAtomicWriteRenameProbeAt(
				t,
				filesRoot,
				internalRoot,
				"checkpoint:journal_pending:"+targetName,
			)
			pendingRel := filepath.Join(
				atomicWriteRenameProbeJournalDir,
				atomicWriteRenameProbePendingName(targetName),
			)
			replaceAtomicWriteRenameProbeJournalFile(t, internalRoot, pendingRel, []byte(`{"schema":`))
			if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
				t.Fatalf("recover partial after-phase pending record: %v", err)
			}
			assertNoAtomicWriteRenameProbeResidue(t, filesRoot, internalRoot)
			assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
		})
	})

	t.Run("semantic pending replacement is preserved", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		interruptAtomicWriteRenameProbeAt(
			t,
			filesRoot,
			internalRoot,
			"checkpoint:journal_pending:setup.json",
		)
		pendingRel := filepath.Join(
			atomicWriteRenameProbeJournalDir,
			atomicWriteRenameProbePendingName(atomicWriteRenameProbeSetupName),
		)
		replaceAtomicWriteRenameProbeJournalFile(t, internalRoot, pendingRel, []byte("{}\n"))
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
			t.Fatalf("probe with semantic pending replacement error = %v, want fail closed", err)
		}
		data, readErr := internalRoot.ReadFile(pendingRel)
		if readErr != nil {
			t.Fatalf("semantic pending replacement was deleted: %v", readErr)
		}
		if got, want := string(data), "{}\n"; got != want {
			t.Fatalf("semantic pending replacement = %q, want %q", got, want)
		}
	})

	t.Run("out-of-order partial pending metadata is preserved", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		interruptAtomicWriteRenameProbeAt(
			t,
			filesRoot,
			internalRoot,
			"checkpoint:setup_intent",
		)
		pendingRel := filepath.Join(
			atomicWriteRenameProbeJournalDir,
			atomicWriteRenameProbePendingName(atomicWriteRenameProbePeerBindingName),
		)
		if err := internalRoot.WriteFile(pendingRel, []byte("{"), 0o600); err != nil {
			t.Fatalf("create out-of-order partial pending binding: %v", err)
		}

		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
			t.Fatalf("probe with out-of-order partial pending error = %v, want fail closed", err)
		}
		if data, readErr := internalRoot.ReadFile(pendingRel); readErr != nil {
			t.Fatalf("out-of-order partial pending binding was deleted: %v", readErr)
		} else if got, want := string(data), "{"; got != want {
			t.Fatalf("out-of-order partial pending binding = %q, want %q", got, want)
		}
	})

	t.Run("complete pending metadata with trailing corruption is preserved", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		pendingName := atomicWriteRenameProbePendingName(
			atomicWriteRenameProbeSourceBindingName,
		)
		interruptAtomicWriteRenameProbeAt(
			t,
			filesRoot,
			internalRoot,
			"checkpoint:journal_pending:"+atomicWriteRenameProbeSourceBindingName,
		)
		pendingRel := filepath.Join(atomicWriteRenameProbeJournalDir, pendingName)
		original, err := internalRoot.ReadFile(pendingRel)
		if err != nil {
			t.Fatalf("read complete pending source binding: %v", err)
		}
		replaceAtomicWriteRenameProbeJournalFile(
			t,
			internalRoot,
			pendingRel,
			append(original, '{'),
		)

		err = probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
			t.Fatalf("probe with trailing pending corruption error = %v, want fail closed", err)
		}
		if _, readErr := internalRoot.ReadFile(pendingRel); readErr != nil {
			t.Fatalf("trailing-corrupt pending binding was deleted: %v", readErr)
		}
	})

	t.Run("unknown exact setup object is preserved", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, "namespace:setup_source_created")
		var setup atomicWriteRenameProbeSetupRecord
		data, err := internalRoot.ReadFile(
			filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSetupName),
		)
		if err != nil {
			t.Fatalf("read setup intent: %v", err)
		}
		if err := json.Unmarshal(data, &setup); err != nil {
			t.Fatalf("decode setup intent: %v", err)
		}
		if err := internalRoot.Remove(setup.Paths.SourceIsolation); err != nil {
			t.Fatalf("remove setup source before replacement: %v", err)
		}
		if err := internalRoot.WriteFile(
			setup.Paths.SourceIsolation,
			[]byte("unknown setup replacement"),
			0o600,
		); err != nil {
			t.Fatalf("create setup source replacement: %v", err)
		}
		err = probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
			!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
			t.Fatalf("probe with setup replacement error = %v, want fail-closed ownership error", err)
		}
		replacement, readErr := internalRoot.ReadFile(setup.Paths.SourceIsolation)
		if readErr != nil {
			t.Fatalf("read preserved setup replacement: %v", readErr)
		}
		if got, want := string(replacement), "unknown setup replacement"; got != want {
			t.Fatalf("preserved setup replacement = %q, want %q", got, want)
		}
	})
}

func TestAtomicWriteRenameProbeJournalRejectsOutOfOrderIncompleteSetup(t *testing.T) {
	tests := []struct {
		name      string
		point     string
		mutate    func(t *testing.T, internalRoot *os.Root, setup atomicWriteRenameProbeSetupRecord)
		preserved func(setup atomicWriteRenameProbeSetupRecord) []string
	}{
		{
			name:  "source binding without object",
			point: "checkpoint:setup_source_bound",
			mutate: func(t *testing.T, internalRoot *os.Root, setup atomicWriteRenameProbeSetupRecord) {
				t.Helper()
				if err := internalRoot.Remove(setup.Paths.SourceIsolation); err != nil {
					t.Fatalf("remove bound source object: %v", err)
				}
			},
			preserved: func(atomicWriteRenameProbeSetupRecord) []string {
				return []string{
					filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSetupName),
					filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSourceBindingName),
				}
			},
		},
		{
			name:  "peer binding without object",
			point: "checkpoint:setup_peer_bound",
			mutate: func(t *testing.T, internalRoot *os.Root, setup atomicWriteRenameProbeSetupRecord) {
				t.Helper()
				if err := internalRoot.Remove(setup.Paths.PeerIsolation); err != nil {
					t.Fatalf("remove bound peer object: %v", err)
				}
			},
			preserved: func(atomicWriteRenameProbeSetupRecord) []string {
				return []string{
					filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSetupName),
					filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbePeerBindingName),
				}
			},
		},
		{
			name:  "peer pending object without source predecessor",
			point: atomicWriteRenameProbeObjectFaultPoint("peer", "after_partial_write"),
			mutate: func(t *testing.T, internalRoot *os.Root, setup atomicWriteRenameProbeSetupRecord) {
				t.Helper()
				if err := internalRoot.Remove(setup.Paths.SourceIsolation); err != nil {
					t.Fatalf("remove source object predecessor: %v", err)
				}
				if err := internalRoot.Remove(
					filepath.Join(
						atomicWriteRenameProbeJournalDir,
						atomicWriteRenameProbeSourceBindingName,
					),
				); err != nil {
					t.Fatalf("remove source binding predecessor: %v", err)
				}
			},
			preserved: func(setup atomicWriteRenameProbeSetupRecord) []string {
				pending, _ := atomicWriteRenameProbeObjectPathsForTest(setup, "peer")
				return []string{
					filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSetupName),
					pending,
				}
			},
		},
		{
			name:  "peer binding without source predecessor",
			point: "checkpoint:setup_peer_bound",
			mutate: func(t *testing.T, internalRoot *os.Root, setup atomicWriteRenameProbeSetupRecord) {
				t.Helper()
				if err := internalRoot.Remove(setup.Paths.SourceIsolation); err != nil {
					t.Fatalf("remove source object predecessor: %v", err)
				}
				if err := internalRoot.Remove(
					filepath.Join(
						atomicWriteRenameProbeJournalDir,
						atomicWriteRenameProbeSourceBindingName,
					),
				); err != nil {
					t.Fatalf("remove source binding predecessor: %v", err)
				}
			},
			preserved: func(setup atomicWriteRenameProbeSetupRecord) []string {
				return []string{
					filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSetupName),
					setup.Paths.PeerIsolation,
					filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbePeerBindingName),
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()

			interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, test.point)
			setup := readAtomicWriteRenameProbeSetupForTest(t, internalRoot)
			test.mutate(t, internalRoot, setup)

			err := probeAtomicWriteRenames(filesRoot, internalRoot)
			if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
				!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
				t.Fatalf("out-of-order setup probe error = %v, want fail-closed ownership error", err)
			}
			for _, rel := range test.preserved(setup) {
				if _, statErr := internalRoot.Lstat(rel); statErr != nil {
					t.Fatalf("out-of-order setup evidence %s was removed: %v", rel, statErr)
				}
			}
		})
	}
}

func TestAtomicWriteRenameProbeJournalRecoversPendingProbeObjectBoundaries(t *testing.T) {
	for _, role := range []string{"source", "peer"} {
		role := role
		for _, stage := range []string{
			"after_create",
			"after_partial_write",
			"after_file_sync",
			"after_publish",
		} {
			stage := stage
			t.Run(role+"/"+stage, func(t *testing.T) {
				filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
				defer closeRoots()

				interruptAtomicWriteRenameProbeAt(
					t,
					filesRoot,
					internalRoot,
					atomicWriteRenameProbeObjectFaultPoint(role, stage),
				)
				setup := readAtomicWriteRenameProbeSetupForTest(t, internalRoot)
				pendingRel, targetRel := atomicWriteRenameProbeObjectPathsForTest(setup, role)
				if stage == "after_publish" {
					if _, err := internalRoot.Lstat(targetRel); err != nil {
						t.Fatalf("published %s object is missing: %v", role, err)
					}
					if _, err := internalRoot.Lstat(pendingRel); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("published %s pending Lstat error = %v, want missing", role, err)
					}
				} else {
					info, err := internalRoot.Lstat(pendingRel)
					if err != nil {
						t.Fatalf("%s pending object is missing after %s: %v", role, stage, err)
					}
					wantSize := int64(0)
					if stage == "after_partial_write" {
						wantSize = atomicWriteRenameProbeNonceSize / 2
					}
					if stage == "after_file_sync" {
						wantSize = atomicWriteRenameProbeNonceSize
					}
					if info.Size() != wantSize {
						t.Fatalf("%s pending size after %s = %d, want %d", role, stage, info.Size(), wantSize)
					}
				}

				if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
					t.Fatalf("recover %s pending object after %s: %v", role, stage, err)
				}
				assertNoAtomicWriteRenameProbeResidue(t, filesRoot, internalRoot)
				assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
			})
		}
	}
}

func TestAtomicWriteRenameProbeJournalPreservesPendingProbeObjectAmbiguity(t *testing.T) {
	t.Run("pending and target coexist", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		interruptAtomicWriteRenameProbeAt(
			t,
			filesRoot,
			internalRoot,
			atomicWriteRenameProbeObjectFaultPoint("source", "after_file_sync"),
		)
		setup := readAtomicWriteRenameProbeSetupForTest(t, internalRoot)
		pendingRel, targetRel := atomicWriteRenameProbeObjectPathsForTest(setup, "source")
		if err := internalRoot.WriteFile(targetRel, []byte("unknown target"), 0o600); err != nil {
			t.Fatalf("create pending-object target replacement: %v", err)
		}
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
			t.Fatalf("pending+target probe error = %v, want fail closed", err)
		}
		for _, rel := range []string{pendingRel, targetRel} {
			if _, statErr := internalRoot.Lstat(rel); statErr != nil {
				t.Fatalf("ambiguous pending-object entry %s was removed: %v", rel, statErr)
			}
		}
	})

	t.Run("multiple pending objects", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		interruptAtomicWriteRenameProbeAt(
			t,
			filesRoot,
			internalRoot,
			atomicWriteRenameProbeObjectFaultPoint("source", "after_partial_write"),
		)
		setup := readAtomicWriteRenameProbeSetupForTest(t, internalRoot)
		sourcePending, _ := atomicWriteRenameProbeObjectPathsForTest(setup, "source")
		peerPending, _ := atomicWriteRenameProbeObjectPathsForTest(setup, "peer")
		if err := internalRoot.WriteFile(peerPending, []byte("unknown peer pending"), 0o600); err != nil {
			t.Fatalf("create second pending object: %v", err)
		}
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
			t.Fatalf("multiple pending-object probe error = %v, want fail closed", err)
		}
		for _, rel := range []string{sourcePending, peerPending} {
			if _, statErr := internalRoot.Lstat(rel); statErr != nil {
				t.Fatalf("multiple pending-object entry %s was removed: %v", rel, statErr)
			}
		}
	})

	t.Run("unknown regular pending content", func(t *testing.T) {
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
		replaceAtomicWriteRenameProbeJournalFile(
			t,
			internalRoot,
			pendingRel,
			[]byte(strings.Repeat("u", atomicWriteRenameProbeNonceSize/2)),
		)
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
			!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
			t.Fatalf("unknown pending-object probe error = %v, want ownership failure", err)
		}
		data, readErr := internalRoot.ReadFile(pendingRel)
		if readErr != nil {
			t.Fatalf("unknown pending object was removed: %v", readErr)
		}
		if got, want := string(data), strings.Repeat("u", atomicWriteRenameProbeNonceSize/2); got != want {
			t.Fatalf("unknown pending object = %q, want %q", got, want)
		}
	})
}

func TestAtomicWriteRenameProbeJournalPendingObjectReplacementFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		stage  string
		action string
		size   int
	}{
		{name: "partial before remove", stage: "after_partial_write", action: "remove", size: atomicWriteRenameProbeNonceSize / 2},
		{name: "complete before publish", stage: "after_file_sync", action: "publish", size: atomicWriteRenameProbeNonceSize},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()
			t.Cleanup(func() {
				atomicWriteRenameProbeBeforePendingObjectAction = func(string, string) error { return nil }
			})
			interruptAtomicWriteRenameProbeAt(
				t,
				filesRoot,
				internalRoot,
				atomicWriteRenameProbeObjectFaultPoint("source", test.stage),
			)
			setup := readAtomicWriteRenameProbeSetupForTest(t, internalRoot)
			pendingRel, _ := atomicWriteRenameProbeObjectPathsForTest(setup, "source")
			nonce, err := hex.DecodeString(setup.Source.Nonce)
			if err != nil {
				t.Fatalf("decode setup source nonce: %v", err)
			}
			replaced := false
			atomicWriteRenameProbeBeforePendingObjectAction = func(role, action string) error {
				if role != "source" || action != test.action || replaced {
					return nil
				}
				replaced = true
				if err := internalRoot.Remove(pendingRel); err != nil {
					return err
				}
				return internalRoot.WriteFile(pendingRel, nonce[:test.size], 0o600)
			}
			err = probeAtomicWriteRenames(filesRoot, internalRoot)
			atomicWriteRenameProbeBeforePendingObjectAction = func(string, string) error { return nil }
			if !replaced {
				t.Fatal("pending-object replacement hook was not reached")
			}
			if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
				!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
				t.Fatalf("pending-object replacement error = %v, want ownership failure", err)
			}
			data, readErr := internalRoot.ReadFile(pendingRel)
			if readErr != nil {
				t.Fatalf("pending-object replacement was removed: %v", readErr)
			}
			if !bytes.Equal(data, nonce[:test.size]) {
				t.Fatal("pending-object replacement content changed")
			}
		})
	}
}

func TestAtomicWriteRenameProbeJournalSyncsInternalParentBeforeMutation(t *testing.T) {
	t.Run("existing journal", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		if err := rootio.MkdirNoFollow(internalRoot, atomicWriteRenameProbeJournalDir, 0o700); err != nil {
			t.Fatalf("create existing probe journal: %v", err)
		}
		t.Cleanup(func() {
			syncAtomicWriteRenameProbeInternalDir = func(dir *os.File) error { return dir.Sync() }
			atomicWriteRenameProbeFaultHook = func(string) error { return nil }
		})
		parentSynced := false
		syncAtomicWriteRenameProbeInternalDir = func(dir *os.File) error {
			if err := dir.Sync(); err != nil {
				return err
			}
			parentSynced = true
			return nil
		}
		injected := errors.New("stop before setup mutation")
		atomicWriteRenameProbeFaultHook = func(point string) error {
			if point == "checkpoint:journal_parent_synced" && !parentSynced {
				return errors.New("parent sync hook ran before parent sync")
			}
			if point == "before:setup_intent" {
				if !parentSynced {
					return errors.New("setup mutation reached before parent sync")
				}
				return injected
			}
			return nil
		}
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, errAtomicWriteRenameProbeCrashInjected) || !errors.Is(err, injected) {
			t.Fatalf("probe stopped before setup mutation error = %v, want injected error", err)
		}
		assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
	})

	t.Run("sync failure prevents mutation", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		if err := rootio.MkdirNoFollow(internalRoot, atomicWriteRenameProbeJournalDir, 0o700); err != nil {
			t.Fatalf("create existing probe journal: %v", err)
		}
		t.Cleanup(func() {
			syncAtomicWriteRenameProbeInternalDir = func(dir *os.File) error { return dir.Sync() }
		})
		syncErr := errors.New("internal parent sync failed")
		syncAtomicWriteRenameProbeInternalDir = func(*os.File) error { return syncErr }
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) || !errors.Is(err, syncErr) {
			t.Fatalf("probe parent-sync failure error = %v, want fail closed sync error", err)
		}
		assertAtomicWriteRenameProbeJournalEmpty(t, internalRoot)
	})

	t.Run("recovery mutation follows parent sync", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		interruptAtomicWriteRenameProbeAt(
			t,
			filesRoot,
			internalRoot,
			atomicWriteRenameProbeObjectFaultPoint("source", "after_partial_write"),
		)
		t.Cleanup(func() {
			syncAtomicWriteRenameProbeInternalDir = func(dir *os.File) error { return dir.Sync() }
			atomicWriteRenameProbeBeforePendingObjectAction = func(string, string) error { return nil }
		})
		parentSynced := false
		syncAtomicWriteRenameProbeInternalDir = func(dir *os.File) error {
			if err := dir.Sync(); err != nil {
				return err
			}
			parentSynced = true
			return nil
		}
		atomicWriteRenameProbeBeforePendingObjectAction = func(string, string) error {
			if !parentSynced {
				return errors.New("pending-object recovery preceded parent sync")
			}
			return nil
		}
		if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
			t.Fatalf("recover pending object after parent sync: %v", err)
		}
	})
}

func TestAtomicWriteRenameProbeJournalPreservesPendingAmbiguity(t *testing.T) {
	tests := []struct {
		name  string
		files []string
	}{
		{
			name: "multiple pending records",
			files: []string{
				atomicWriteRenameProbePendingName(atomicWriteRenameProbeSetupName),
				atomicWriteRenameProbePendingName(atomicWriteRenameProbeManifestName),
			},
		},
		{
			name: "pending and target coexist",
			files: []string{
				atomicWriteRenameProbePendingName(atomicWriteRenameProbeSetupName),
				atomicWriteRenameProbeSetupName,
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
			for _, name := range test.files {
				if err := internalRoot.WriteFile(
					filepath.Join(atomicWriteRenameProbeJournalDir, name),
					[]byte("{}\n"),
					0o600,
				); err != nil {
					t.Fatalf("create ambiguous journal record %s: %v", name, err)
				}
			}
			err := probeAtomicWriteRenames(filesRoot, internalRoot)
			if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
				t.Fatalf("probe with ambiguous pending records error = %v, want fail closed", err)
			}
			for _, name := range test.files {
				if _, statErr := internalRoot.Lstat(
					filepath.Join(atomicWriteRenameProbeJournalDir, name),
				); statErr != nil {
					t.Fatalf("ambiguous journal record %s was removed: %v", name, statErr)
				}
			}
		})
	}
}

func TestAtomicWriteRenameProbeJournalCheckedRemovalPreservesLateReplacement(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()
	t.Cleanup(func() {
		atomicWriteRenameProbeBeforeCheckedRemove = func(string) error { return nil }
	})
	interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, "checkpoint:recovery_source_isolated")

	manifest := readAtomicWriteRenameProbeManifestForTest(t, internalRoot)
	replaced := false
	atomicWriteRenameProbeBeforeCheckedRemove = func(label string) error {
		if label != "recovery_source" || replaced {
			return nil
		}
		replaced = true
		if err := internalRoot.Remove(manifest.Paths.SourceIsolation); err != nil {
			return err
		}
		return internalRoot.WriteFile(
			manifest.Paths.SourceIsolation,
			[]byte(strings.Repeat("u", atomicWriteRenameProbeNonceSize)),
			0o600,
		)
	}
	err := probeAtomicWriteRenames(filesRoot, internalRoot)
	atomicWriteRenameProbeBeforeCheckedRemove = func(string) error { return nil }
	if !replaced {
		t.Fatal("checked-removal replacement hook was not reached")
	}
	if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
		!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
		t.Fatalf("probe after late replacement error = %v, want fail-closed ownership error", err)
	}
	data, readErr := internalRoot.ReadFile(manifest.Paths.SourceIsolation)
	if readErr != nil {
		t.Fatalf("late replacement was deleted: %v", readErr)
	}
	if got, want := string(data), strings.Repeat("u", atomicWriteRenameProbeNonceSize); got != want {
		t.Fatalf("late replacement = %q, want %q", got, want)
	}
}

func TestAtomicWriteRenameProbeJournalMetadataRemovalPreservesLateReplacement(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()
	t.Cleanup(func() {
		atomicWriteRenameProbeBeforeCheckedRemove = func(string) error { return nil }
	})

	targetName := atomicWriteRenameProbePhaseName(0)
	targetRel := filepath.Join(atomicWriteRenameProbeJournalDir, targetName)
	replaced := false
	atomicWriteRenameProbeBeforeCheckedRemove = func(label string) error {
		if label != "metadata:"+targetName || replaced {
			return nil
		}
		replaced = true
		if err := internalRoot.Remove(targetRel); err != nil {
			return err
		}
		return internalRoot.WriteFile(targetRel, []byte("unknown metadata replacement"), 0o600)
	}
	err := probeAtomicWriteRenames(filesRoot, internalRoot)
	atomicWriteRenameProbeBeforeCheckedRemove = func(string) error { return nil }
	if !replaced {
		t.Fatal("metadata checked-removal replacement hook was not reached")
	}
	if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
		!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
		t.Fatalf("probe after metadata replacement error = %v, want fail-closed ownership error", err)
	}
	data, readErr := internalRoot.ReadFile(targetRel)
	if readErr != nil {
		t.Fatalf("metadata replacement was deleted: %v", readErr)
	}
	if got, want := string(data), "unknown metadata replacement"; got != want {
		t.Fatalf("metadata replacement = %q, want %q", got, want)
	}
}

func TestAtomicWriteRenameProbeJournalMetadataRemovalPreservesReplacementAfterSemanticRead(t *testing.T) {
	tests := []struct {
		name       string
		targetName string
		prepare    func(t *testing.T, filesRoot, internalRoot *os.Root)
	}{
		{
			name:       "setup",
			targetName: atomicWriteRenameProbeSetupName,
			prepare: func(t *testing.T, filesRoot, internalRoot *os.Root) {
				t.Helper()
				interruptAtomicWriteRenameProbeAt(
					t,
					filesRoot,
					internalRoot,
					"checkpoint:setup_intent",
				)
			},
		},
		{
			name:       "binding",
			targetName: atomicWriteRenameProbeSourceBindingName,
			prepare: func(t *testing.T, filesRoot, internalRoot *os.Root) {
				t.Helper()
				interruptAtomicWriteRenameProbeAt(
					t,
					filesRoot,
					internalRoot,
					"checkpoint:setup_source_bound",
				)
			},
		},
		{
			name:       "phase",
			targetName: atomicWriteRenameProbePhaseName(0),
			prepare: func(t *testing.T, filesRoot, internalRoot *os.Root) {
				t.Helper()
				interruptAtomicWriteRenameProbeAt(
					t,
					filesRoot,
					internalRoot,
					"namespace:exchange_forward",
				)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()
			t.Cleanup(func() {
				atomicWriteRenameProbeBeforeMetadataRemove = func(string) error { return nil }
			})
			test.prepare(t, filesRoot, internalRoot)

			targetRel := filepath.Join(atomicWriteRenameProbeJournalDir, test.targetName)
			replacementContent := []byte("unknown metadata replacement after semantic read")
			replaced := false
			atomicWriteRenameProbeBeforeMetadataRemove = func(name string) error {
				if name != test.targetName || replaced {
					return nil
				}
				replaced = true
				if err := internalRoot.Remove(targetRel); err != nil {
					return err
				}
				return internalRoot.WriteFile(targetRel, replacementContent, 0o600)
			}

			err := probeAtomicWriteRenames(filesRoot, internalRoot)
			atomicWriteRenameProbeBeforeMetadataRemove = func(string) error { return nil }
			if !replaced {
				t.Fatal("metadata replacement hook after semantic read was not reached")
			}
			if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
				!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
				t.Fatalf("metadata replacement error = %v, want fail-closed ownership error", err)
			}
			data, readErr := internalRoot.ReadFile(targetRel)
			if readErr != nil {
				t.Fatalf("metadata replacement after semantic read was removed: %v", readErr)
			}
			if !bytes.Equal(data, replacementContent) {
				t.Fatalf("metadata replacement after semantic read = %q, want %q", data, replacementContent)
			}
		})
	}
}

func TestAtomicWriteRenameProbeJournalRejectsReplacementBetweenRepeatedSemanticReads(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()
	t.Cleanup(func() {
		atomicWriteRenameProbeAfterJournalSemanticRead = func(string) error { return nil }
	})
	interruptAtomicWriteRenameProbeAt(
		t,
		filesRoot,
		internalRoot,
		"namespace:exchange_forward",
	)

	targetName := atomicWriteRenameProbePhaseName(0)
	targetRel := filepath.Join(atomicWriteRenameProbeJournalDir, targetName)
	original, err := internalRoot.ReadFile(targetRel)
	if err != nil {
		t.Fatalf("read phase before repeated semantic-read replacement: %v", err)
	}
	replaced := false
	atomicWriteRenameProbeAfterJournalSemanticRead = func(name string) error {
		if name != targetName || replaced {
			return nil
		}
		replaced = true
		if err := internalRoot.Remove(targetRel); err != nil {
			return err
		}
		return internalRoot.WriteFile(targetRel, original, 0o600)
	}

	err = probeAtomicWriteRenames(filesRoot, internalRoot)
	atomicWriteRenameProbeAfterJournalSemanticRead = func(string) error { return nil }
	if !replaced {
		t.Fatal("repeated semantic-read replacement hook was not reached")
	}
	if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
		!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
		t.Fatalf("repeated semantic-read replacement error = %v, want fail-closed ownership error", err)
	}
	data, readErr := internalRoot.ReadFile(targetRel)
	if readErr != nil {
		t.Fatalf("replacement between repeated semantic reads was removed: %v", readErr)
	}
	if !bytes.Equal(data, original) {
		t.Fatal("replacement between repeated semantic reads changed")
	}
}

func TestAtomicWriteRenameProbeJournalNeverScansOrDeletesPrefixMatches(t *testing.T) {
	filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
	defer closeRoots()

	unrelated := atomicWriteRenameProbePrefix + "user-owned"
	if err := filesRoot.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatalf("create unrelated prefix match: %v", err)
	}
	interruptAtomicWriteRenameProbeAt(t, filesRoot, internalRoot, "namespace:exchange_forward")

	atomicWriteRenameProbeFaultHook = func(string) error { return nil }
	if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
		t.Fatalf("recover probe with unrelated prefix match: %v", err)
	}
	data, err := filesRoot.ReadFile(unrelated)
	if err != nil {
		t.Fatalf("read unrelated prefix match after recovery: %v", err)
	}
	if got, want := string(data), "keep"; got != want {
		t.Fatalf("unrelated prefix match = %q, want %q", got, want)
	}
}

func TestAtomicWriteRenameProbeJournalDirectoryFailsClosedForSymlinkAndWrongMode(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, internalRoot *os.Root)
	}{
		{
			name: "symlink",
			setup: func(t *testing.T, internalRoot *os.Root) {
				t.Helper()
				target := t.TempDir()
				if err := os.Symlink(target, filepath.Join(internalRoot.Name(), atomicWriteRenameProbeJournalDir)); err != nil {
					t.Fatalf("create journal symlink: %v", err)
				}
			},
		},
		{
			name: "wrong mode",
			setup: func(t *testing.T, internalRoot *os.Root) {
				t.Helper()
				if err := internalRoot.Mkdir(atomicWriteRenameProbeJournalDir, 0o755); err != nil {
					t.Fatalf("create wrong-mode journal directory: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
			defer closeRoots()
			test.setup(t, internalRoot)
			err := probeAtomicWriteRenames(filesRoot, internalRoot)
			if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
				t.Fatalf("probe with invalid journal directory error = %v, want ErrWriteAtomicRenameUnsupported", err)
			}
		})
	}
}

func TestAtomicWriteRenameProbeStagingDirectoryFailsClosedForWrongModeAndReplacement(t *testing.T) {
	t.Run("wrong mode", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		if err := os.Chmod(
			filepath.Join(internalRoot.Name(), writeStagingDir),
			0o755,
		); err != nil {
			t.Fatalf("change write staging mode: %v", err)
		}
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) {
			t.Fatalf("probe with wrong-mode write staging error = %v, want fail closed", err)
		}
	})

	t.Run("replacement after setup intent", func(t *testing.T) {
		filesRoot, internalRoot, closeRoots := setupAtomicWriteRenameProbeRoots(t)
		defer closeRoots()
		replaced := false
		atomicWriteRenameProbeFaultHook = func(point string) error {
			if point != "checkpoint:setup_intent" || replaced {
				return nil
			}
			replaced = true
			if err := internalRoot.Rename(writeStagingDir, writeStagingDir+"-original"); err != nil {
				return err
			}
			return internalRoot.Mkdir(writeStagingDir, 0o700)
		}
		err := probeAtomicWriteRenames(filesRoot, internalRoot)
		atomicWriteRenameProbeFaultHook = func(string) error { return nil }
		if !replaced {
			t.Fatal("write staging replacement hook was not reached")
		}
		if !errors.Is(err, ErrWriteAtomicRenameUnsupported) ||
			!errors.Is(err, errAtomicWriteRenameProbeOwnershipChanged) {
			t.Fatalf("probe after write staging replacement error = %v, want ownership failure", err)
		}
		if _, statErr := internalRoot.Lstat(
			filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSetupName),
		); statErr != nil {
			t.Fatalf("setup evidence was removed after write staging replacement: %v", statErr)
		}
	})
}

func interruptAtomicWriteRenameProbeAt(
	t *testing.T,
	filesRoot, internalRoot *os.Root,
	point string,
) {
	t.Helper()
	injected := errors.New("simulated abrupt termination at " + point)
	hit := false
	atomicWriteRenameProbeFaultHook = func(current string) error {
		if current == point {
			hit = true
			return injected
		}
		return nil
	}
	err := probeAtomicWriteRenames(filesRoot, internalRoot)
	atomicWriteRenameProbeFaultHook = func(string) error { return nil }
	if !hit {
		t.Fatalf("probe did not reach interruption point %q", point)
	}
	if !errors.Is(err, errAtomicWriteRenameProbeCrashInjected) || !errors.Is(err, injected) {
		t.Fatalf("interrupted probe error = %v, want injected crash error", err)
	}
}

func assertAtomicWriteRenameProbeJournalEmpty(t *testing.T, internalRoot *os.Root) {
	t.Helper()
	entries := mustReadRootDir(t, internalRoot, atomicWriteRenameProbeJournalDir)
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("atomic write rename probe journal entries = %v, want empty", names)
	}
}

func replaceAtomicWriteRenameProbeJournalFile(
	t *testing.T,
	internalRoot *os.Root,
	rel string,
	data []byte,
) {
	t.Helper()
	file, err := rootio.OpenFileNoFollow(internalRoot, rel, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("open journal record for replacement: %v", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatalf("write replacement journal record: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatalf("sync replacement journal record: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close replacement journal record: %v", err)
	}
}

func readAtomicWriteRenameProbeManifestForTest(
	t *testing.T,
	internalRoot *os.Root,
) atomicWriteRenameProbeJournalManifest {
	t.Helper()
	data, err := internalRoot.ReadFile(
		filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeManifestName),
	)
	if err != nil {
		t.Fatalf("read probe manifest: %v", err)
	}
	var manifest atomicWriteRenameProbeJournalManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode probe manifest: %v", err)
	}
	return manifest
}

func readAtomicWriteRenameProbeSetupForTest(
	t *testing.T,
	internalRoot *os.Root,
) atomicWriteRenameProbeSetupRecord {
	t.Helper()
	data, err := internalRoot.ReadFile(
		filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSetupName),
	)
	if err != nil {
		t.Fatalf("read probe setup intent: %v", err)
	}
	var setup atomicWriteRenameProbeSetupRecord
	if err := json.Unmarshal(data, &setup); err != nil {
		t.Fatalf("decode probe setup intent: %v", err)
	}
	return setup
}

func atomicWriteRenameProbeObjectPathsForTest(
	setup atomicWriteRenameProbeSetupRecord,
	role string,
) (string, string) {
	targetRel := setup.Paths.SourceIsolation
	if role == "peer" {
		targetRel = setup.Paths.PeerIsolation
	}
	return filepath.Join(
		atomicWriteRenameProbeJournalDir,
		atomicWriteRenameProbePendingName(filepath.Base(targetRel)),
	), targetRel
}
