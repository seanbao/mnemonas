package storage

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/workspace"
)

const (
	atomicWriteRenameProbeJournalDir         = "atomic-write-rename-probe-journal"
	atomicWriteRenameProbeSetupName          = "setup.json"
	atomicWriteRenameProbeSourceBindingName  = "source-bound.json"
	atomicWriteRenameProbePeerBindingName    = "peer-bound.json"
	atomicWriteRenameProbeManifestName       = "operation.json"
	atomicWriteRenameProbeRecoveryName       = "recovery.json"
	atomicWriteRenameProbeCleanupName        = "cleanup.json"
	atomicWriteRenameProbeSourceIsolation    = "source.object"
	atomicWriteRenameProbePeerIsolation      = "peer.object"
	atomicWriteRenameProbePendingPrefix      = "pending-"
	atomicWriteRenameProbeJournalSchema      = 1
	atomicWriteRenameProbeMaxJournalFileSize = 64 << 10
)

var errAtomicWriteRenameProbeCrashInjected = errors.New("atomic write rename probe crash injected")
var errAtomicWriteRenameProbePendingRecordIncomplete = errors.New("atomic write rename probe pending record is incomplete")

// atomicWriteRenameProbeFaultHook is replaced only by package tests. Returning
// an error simulates abrupt termination after the named durable boundary.
var atomicWriteRenameProbeFaultHook = func(string) error { return nil }

// syncAtomicWriteRenameProbeInternalDir is replaced only by package tests to
// verify the startup parent-directory durability barrier.
var syncAtomicWriteRenameProbeInternalDir = func(dir *os.File) error {
	return dir.Sync()
}

// atomicWriteRenameProbeBeforeCheckedRemove is replaced only by package tests
// to exercise same-name replacement between validation and checked unlink.
var atomicWriteRenameProbeBeforeCheckedRemove = func(string) error { return nil }

// atomicWriteRenameProbeBeforePendingObjectAction is replaced only by package
// tests to exercise replacement between pending-object validation and mutation.
var atomicWriteRenameProbeBeforePendingObjectAction = func(string, string) error { return nil }

// atomicWriteRenameProbeBeforeMetadataRemove is replaced only by package tests
// to exercise replacement after semantic validation and before removal opens
// the current journal entry.
var atomicWriteRenameProbeBeforeMetadataRemove = func(string) error { return nil }

// atomicWriteRenameProbeAfterJournalSemanticRead is replaced only by package
// tests to exercise replacement between repeated semantic reads.
var atomicWriteRenameProbeAfterJournalSemanticRead = func(string) error { return nil }

type atomicWriteRenameProbeJournalRoot struct {
	PersistentIdentity string `json:"persistent_identity"`
	Mode               uint32 `json:"mode"`
}

type atomicWriteRenameProbeJournalObject struct {
	Nonce              string `json:"nonce"`
	SHA256             string `json:"sha256"`
	PersistentIdentity string `json:"persistent_identity"`
	Mode               uint32 `json:"mode"`
	Size               int64  `json:"size"`
	ModTimeUnixNano    int64  `json:"mod_time_unix_nano"`
}

type atomicWriteRenameProbeSetupObject struct {
	Nonce  string `json:"nonce"`
	SHA256 string `json:"sha256"`
}

type atomicWriteRenameProbeJournalPaths struct {
	Source          string `json:"source"`
	Peer            string `json:"peer"`
	FilesSlot       string `json:"files_slot"`
	SourceIsolation string `json:"source_isolation"`
	PeerIsolation   string `json:"peer_isolation"`
}

type atomicWriteRenameProbeSetupRecord struct {
	Schema       int                                `json:"schema"`
	OperationID  string                             `json:"operation_id"`
	FilesRoot    atomicWriteRenameProbeJournalRoot  `json:"files_root"`
	InternalRoot atomicWriteRenameProbeJournalRoot  `json:"internal_root"`
	StagingRoot  atomicWriteRenameProbeJournalRoot  `json:"staging_root"`
	JournalRoot  atomicWriteRenameProbeJournalRoot  `json:"journal_root"`
	Paths        atomicWriteRenameProbeJournalPaths `json:"paths"`
	Source       atomicWriteRenameProbeSetupObject  `json:"source"`
	Peer         atomicWriteRenameProbeSetupObject  `json:"peer"`
}

type atomicWriteRenameProbeBindingRecord struct {
	Schema      int                                 `json:"schema"`
	OperationID string                              `json:"operation_id"`
	Role        string                              `json:"role"`
	Object      atomicWriteRenameProbeJournalObject `json:"object"`
}

type atomicWriteRenameProbeJournalManifest struct {
	Schema       int                                 `json:"schema"`
	OperationID  string                              `json:"operation_id"`
	FilesRoot    atomicWriteRenameProbeJournalRoot   `json:"files_root"`
	InternalRoot atomicWriteRenameProbeJournalRoot   `json:"internal_root"`
	StagingRoot  atomicWriteRenameProbeJournalRoot   `json:"staging_root"`
	JournalRoot  atomicWriteRenameProbeJournalRoot   `json:"journal_root"`
	Paths        atomicWriteRenameProbeJournalPaths  `json:"paths"`
	Source       atomicWriteRenameProbeJournalObject `json:"source"`
	Peer         atomicWriteRenameProbeJournalObject `json:"peer"`
}

type atomicWriteRenameProbePhaseRecord struct {
	Schema      int    `json:"schema"`
	OperationID string `json:"operation_id"`
	Index       int    `json:"index"`
	Phase       string `json:"phase"`
}

type atomicWriteRenameProbeRecoveryRecord struct {
	Schema      int    `json:"schema"`
	OperationID string `json:"operation_id"`
	SourceFrom  string `json:"source_from"`
	PeerFrom    string `json:"peer_from"`
}

type atomicWriteRenameProbeCleanupRecord struct {
	Schema   int                                   `json:"schema"`
	Manifest atomicWriteRenameProbeJournalManifest `json:"manifest"`
}

type atomicWriteRenameProbeJournalContext struct {
	filesRoot            *os.Root
	internalRoot         *os.Root
	filesDir             *os.File
	stagingDir           *os.File
	internalDir          *os.File
	journalDir           *os.File
	journalLocked        bool
	validatedJournalFile map[string]atomicWriteRenameProbeJournalFileEvidence
	rename               atomicWriteRenameFunc
	exchange             atomicWriteRenameFunc
}

type atomicWriteRenameProbeJournalFileEvidence struct {
	file atomicWriteRenameProbeEvidence
	hash [sha256.Size]byte
}

type atomicWriteRenameProbeLayout struct {
	source string
	peer   string
}

type atomicWriteRenameProbePhaseDefinition struct {
	name    string
	current atomicWriteRenameProbeLayout
	next    atomicWriteRenameProbeLayout
}

type atomicWriteRenameProbeObservedLayout struct {
	source string
	peer   string
}

const (
	atomicWriteRenameProbeLocationMissing         = "missing"
	atomicWriteRenameProbeLocationSource          = "source"
	atomicWriteRenameProbeLocationPeer            = "peer"
	atomicWriteRenameProbeLocationFiles           = "files"
	atomicWriteRenameProbeLocationSourceIsolation = "source_isolation"
	atomicWriteRenameProbeLocationPeerIsolation   = "peer_isolation"
)

var atomicWriteRenameProbePhases = []atomicWriteRenameProbePhaseDefinition{
	{
		name:    "objects_bound",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSourceIsolation, peer: atomicWriteRenameProbeLocationPeerIsolation},
	},
	{
		name:    "prepare_source_to_staging",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSourceIsolation, peer: atomicWriteRenameProbeLocationPeerIsolation},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeerIsolation},
	},
	{
		name:    "source_in_staging",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeerIsolation},
	},
	{
		name:    "prepare_peer_to_staging",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeerIsolation},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeer},
	},
	{
		name:    "objects_in_staging",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeer},
	},
	{
		name:    "prepare_peer_to_files_first",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeer},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "peer_in_files_first",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "prepare_reject_internal_to_files",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "rejected_internal_to_files",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "prepare_reject_files_to_internal",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "rejected_files_to_internal",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "prepare_peer_to_internal_first",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeer},
	},
	{
		name:    "objects_home_first",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeer},
	},
	{
		name:    "prepare_peer_to_files_second",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeer},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "peer_in_files_second",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "prepare_exchange_forward",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationFiles, peer: atomicWriteRenameProbeLocationSource},
	},
	{
		name:    "exchange_forward",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationFiles, peer: atomicWriteRenameProbeLocationSource},
	},
	{
		name:    "prepare_exchange_reverse",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationFiles, peer: atomicWriteRenameProbeLocationSource},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "exchange_restored",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
	},
	{
		name:    "prepare_peer_to_internal_second",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationFiles},
		next:    atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeer},
	},
	{
		name:    "objects_home_second",
		current: atomicWriteRenameProbeLayout{source: atomicWriteRenameProbeLocationSource, peer: atomicWriteRenameProbeLocationPeer},
	},
}

func probeAtomicWriteRenamesJournaled(
	filesRoot, internalRoot *os.Root,
	rename atomicWriteRenameFunc,
	exchange atomicWriteRenameFunc,
) (resultErr error) {
	ctx, err := openAtomicWriteRenameProbeJournalContext(filesRoot, internalRoot, rename, exchange)
	if err != nil {
		return atomicWriteRenameProbeJournalFailure("open probe journal", err)
	}
	defer func() {
		if closeErr := ctx.close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close atomic write rename probe journal: %w", closeErr))
		}
	}()

	if err := ctx.recoverExistingOperation(); err != nil {
		return atomicWriteRenameProbeJournalFailure("recover interrupted probe", err)
	}

	manifest, owners, err := ctx.beginOperation()
	if err != nil {
		for _, owner := range owners {
			if owner != nil && owner.file != nil {
				_ = owner.file.Close()
			}
		}
		if errors.Is(err, errAtomicWriteRenameProbeCrashInjected) {
			return err
		}
		recoveryErr := ctx.recoverExistingOperation()
		if recoveryErr != nil {
			recoveryErr = atomicWriteRenameProbeJournalFailure("recover incomplete probe setup", recoveryErr)
		}
		return errors.Join(fmt.Errorf("begin atomic write rename probe journal: %w", err), recoveryErr)
	}
	defer func() {
		for _, owner := range owners {
			if owner != nil && owner.file != nil {
				resultErr = errors.Join(resultErr, owner.file.Close())
			}
		}
	}()

	runErr := ctx.runOperation(manifest)
	if errors.Is(runErr, errAtomicWriteRenameProbeCrashInjected) {
		return runErr
	}
	recoveryErr := ctx.recoverExistingOperation()
	if recoveryErr != nil {
		recoveryErr = atomicWriteRenameProbeJournalFailure("recover current probe", recoveryErr)
	}
	return errors.Join(runErr, recoveryErr)
}

func openAtomicWriteRenameProbeJournalContext(
	filesRoot, internalRoot *os.Root,
	rename atomicWriteRenameFunc,
	exchange atomicWriteRenameFunc,
) (*atomicWriteRenameProbeJournalContext, error) {
	filesDir, err := rootio.OpenDirNoFollow(filesRoot, ".")
	if err != nil {
		return nil, fmt.Errorf("open files root for atomic write rename probe: %w", err)
	}
	stagingDir, err := rootio.OpenDirNoFollow(internalRoot, writeStagingDir)
	if err != nil {
		_ = filesDir.Close()
		return nil, fmt.Errorf("open internal write staging for atomic write rename probe: %w", err)
	}
	stagingLstatInfo, err := internalRoot.Lstat(writeStagingDir)
	if err != nil {
		_ = filesDir.Close()
		_ = stagingDir.Close()
		return nil, fmt.Errorf("inspect internal write staging for atomic write rename probe: %w", err)
	}
	stagingInfo, err := stagingDir.Stat()
	if err != nil || !stagingLstatInfo.IsDir() ||
		!os.SameFile(stagingLstatInfo, stagingInfo) ||
		stagingLstatInfo.Mode() != os.ModeDir|0o700 ||
		stagingInfo.Mode() != os.ModeDir|0o700 {
		_ = filesDir.Close()
		_ = stagingDir.Close()
		if err != nil {
			return nil, fmt.Errorf("inspect opened internal write staging for atomic write rename probe: %w", err)
		}
		return nil, errors.New("atomic write rename probe staging directory identity or mode is invalid")
	}
	internalDir, err := rootio.OpenDirNoFollow(internalRoot, ".")
	if err != nil {
		_ = filesDir.Close()
		_ = stagingDir.Close()
		return nil, fmt.Errorf("open internal root for atomic write rename probe: %w", err)
	}

	if err := rootio.MkdirNoFollow(internalRoot, atomicWriteRenameProbeJournalDir, 0o700); err != nil &&
		!errors.Is(err, os.ErrExist) {
		_ = filesDir.Close()
		_ = stagingDir.Close()
		_ = internalDir.Close()
		return nil, fmt.Errorf("create atomic write rename probe journal directory: %w", err)
	}
	journalDir, err := rootio.OpenDirNoFollow(internalRoot, atomicWriteRenameProbeJournalDir)
	if err != nil {
		_ = filesDir.Close()
		_ = stagingDir.Close()
		_ = internalDir.Close()
		return nil, fmt.Errorf("open atomic write rename probe journal directory: %w", err)
	}
	lstatInfo, err := internalRoot.Lstat(atomicWriteRenameProbeJournalDir)
	if err != nil {
		_ = filesDir.Close()
		_ = stagingDir.Close()
		_ = internalDir.Close()
		_ = journalDir.Close()
		return nil, fmt.Errorf("inspect atomic write rename probe journal directory: %w", err)
	}
	journalInfo, err := journalDir.Stat()
	if err != nil || !lstatInfo.IsDir() || !os.SameFile(lstatInfo, journalInfo) ||
		journalInfo.Mode() != os.ModeDir|0o700 {
		_ = filesDir.Close()
		_ = stagingDir.Close()
		_ = internalDir.Close()
		_ = journalDir.Close()
		if err != nil {
			return nil, fmt.Errorf("inspect opened atomic write rename probe journal directory: %w", err)
		}
		return nil, errors.New("atomic write rename probe journal directory identity or mode is invalid")
	}
	if err := tryLockAtomicWriteRenameProbeJournal(journalDir); err != nil {
		_ = filesDir.Close()
		_ = stagingDir.Close()
		_ = internalDir.Close()
		_ = journalDir.Close()
		return nil, fmt.Errorf("lock atomic write rename probe journal: %w", err)
	}
	if err := syncAtomicWriteRenameProbeInternalDir(internalDir); err != nil {
		_ = unlockAtomicWriteRenameProbeJournal(journalDir)
		_ = filesDir.Close()
		_ = stagingDir.Close()
		_ = internalDir.Close()
		_ = journalDir.Close()
		return nil, fmt.Errorf("sync internal root before atomic write rename probe recovery: %w", err)
	}
	if err := callAtomicWriteRenameProbeFaultHook("checkpoint:journal_parent_synced"); err != nil {
		_ = unlockAtomicWriteRenameProbeJournal(journalDir)
		_ = filesDir.Close()
		_ = stagingDir.Close()
		_ = internalDir.Close()
		_ = journalDir.Close()
		return nil, err
	}

	return &atomicWriteRenameProbeJournalContext{
		filesRoot:            filesRoot,
		internalRoot:         internalRoot,
		filesDir:             filesDir,
		stagingDir:           stagingDir,
		internalDir:          internalDir,
		journalDir:           journalDir,
		journalLocked:        true,
		validatedJournalFile: make(map[string]atomicWriteRenameProbeJournalFileEvidence),
		rename:               rename,
		exchange:             exchange,
	}, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) close() error {
	if ctx == nil {
		return nil
	}
	var unlockErr error
	if ctx.journalLocked {
		unlockErr = unlockAtomicWriteRenameProbeJournal(ctx.journalDir)
		ctx.journalLocked = false
	}
	return errors.Join(
		unlockErr,
		ctx.filesDir.Close(),
		ctx.stagingDir.Close(),
		ctx.internalDir.Close(),
		ctx.journalDir.Close(),
	)
}

func (ctx *atomicWriteRenameProbeJournalContext) beginOperation() (
	*atomicWriteRenameProbeJournalManifest,
	[]*atomicWriteRenameProbeFile,
	error,
) {
	entries, err := ctx.readJournalDir()
	if err != nil {
		return nil, nil, err
	}
	if len(entries) != 0 {
		return nil, nil, errors.New("atomic write rename probe journal is not empty after recovery")
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return nil, nil, err
	}

	operationIDBytes := make([]byte, 16)
	if _, err := rand.Read(operationIDBytes); err != nil {
		return nil, nil, err
	}
	operationID := hex.EncodeToString(operationIDBytes)
	paths := atomicWriteRenameProbeJournalPaths{
		Source:          filepath.Join(writeStagingDir, atomicWriteRenameProbePrefix+operationID+"-source"),
		Peer:            filepath.Join(writeStagingDir, atomicWriteRenameProbePrefix+operationID+"-peer"),
		FilesSlot:       atomicWriteRenameProbePrefix + operationID + "-slot",
		SourceIsolation: filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSourceIsolation),
		PeerIsolation:   filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbePeerIsolation),
	}
	for _, location := range []struct {
		root *os.Root
		rel  string
	}{
		{ctx.internalRoot, paths.Source},
		{ctx.internalRoot, paths.Peer},
		{ctx.filesRoot, paths.FilesSlot},
		{ctx.internalRoot, paths.SourceIsolation},
		{ctx.internalRoot, paths.PeerIsolation},
	} {
		if _, err := location.root.Lstat(location.rel); err == nil {
			return nil, nil, fmt.Errorf("atomic write rename probe slot already exists: %s", location.rel)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("inspect atomic write rename probe slot %s: %w", location.rel, err)
		}
	}

	filesRootIdentity, err := atomicWriteRenameProbeRootIdentity(ctx.filesDir)
	if err != nil {
		return nil, nil, fmt.Errorf("bind files root identity: %w", err)
	}
	internalRootIdentity, err := atomicWriteRenameProbeRootIdentity(ctx.internalDir)
	if err != nil {
		return nil, nil, fmt.Errorf("bind internal root identity: %w", err)
	}
	stagingRootIdentity, err := atomicWriteRenameProbeRootIdentity(ctx.stagingDir)
	if err != nil {
		return nil, nil, fmt.Errorf("bind write staging identity: %w", err)
	}
	journalRootIdentity, err := atomicWriteRenameProbeRootIdentity(ctx.journalDir)
	if err != nil {
		return nil, nil, fmt.Errorf("bind probe journal identity: %w", err)
	}

	var sourceNonce [atomicWriteRenameProbeNonceSize]byte
	if _, err := rand.Read(sourceNonce[:]); err != nil {
		return nil, nil, err
	}
	var peerNonce [atomicWriteRenameProbeNonceSize]byte
	if _, err := rand.Read(peerNonce[:]); err != nil {
		return nil, nil, err
	}
	if sourceNonce == peerNonce {
		return nil, nil, errors.New("atomic write rename probe nonces are not independent")
	}
	setup := atomicWriteRenameProbeSetupRecord{
		Schema:       atomicWriteRenameProbeJournalSchema,
		OperationID:  operationID,
		FilesRoot:    filesRootIdentity,
		InternalRoot: internalRootIdentity,
		StagingRoot:  stagingRootIdentity,
		JournalRoot:  journalRootIdentity,
		Paths:        paths,
		Source:       atomicWriteRenameProbeSetupObjectFromNonce(sourceNonce),
		Peer:         atomicWriteRenameProbeSetupObjectFromNonce(peerNonce),
	}
	if err := validateAtomicWriteRenameProbeSetupRecord(setup); err != nil {
		return nil, nil, err
	}
	if err := callAtomicWriteRenameProbeFaultHook("before:setup_intent"); err != nil {
		return nil, nil, err
	}
	if err := ctx.writeJournalJSON(atomicWriteRenameProbeSetupName, setup); err != nil {
		return nil, nil, fmt.Errorf("persist atomic write rename probe setup intent: %w", err)
	}
	if err := callAtomicWriteRenameProbeFaultHook("checkpoint:setup_intent"); err != nil {
		return nil, nil, err
	}

	source, err := ctx.createBoundObject("source", paths.SourceIsolation, sourceNonce)
	if err != nil {
		return nil, nil, fmt.Errorf("create journaled source object: %w", err)
	}
	owners := []*atomicWriteRenameProbeFile{source}
	if err := callAtomicWriteRenameProbeFaultHook("namespace:setup_source_created"); err != nil {
		return nil, owners, err
	}
	sourceBinding := atomicWriteRenameProbeBindingRecord{
		Schema:      atomicWriteRenameProbeJournalSchema,
		OperationID: operationID,
		Role:        "source",
		Object:      atomicWriteRenameProbeObjectManifest(source),
	}
	if err := ctx.writeJournalJSON(atomicWriteRenameProbeSourceBindingName, sourceBinding); err != nil {
		return nil, owners, fmt.Errorf("persist atomic write rename probe source binding: %w", err)
	}
	if err := callAtomicWriteRenameProbeFaultHook("checkpoint:setup_source_bound"); err != nil {
		return nil, owners, err
	}

	peer, err := ctx.createBoundObject("peer", paths.PeerIsolation, peerNonce)
	if err != nil {
		return nil, owners, fmt.Errorf("create journaled peer object: %w", err)
	}
	owners = append(owners, peer)
	if err := callAtomicWriteRenameProbeFaultHook("namespace:setup_peer_created"); err != nil {
		return nil, owners, err
	}
	peerBinding := atomicWriteRenameProbeBindingRecord{
		Schema:      atomicWriteRenameProbeJournalSchema,
		OperationID: operationID,
		Role:        "peer",
		Object:      atomicWriteRenameProbeObjectManifest(peer),
	}
	if err := ctx.writeJournalJSON(atomicWriteRenameProbePeerBindingName, peerBinding); err != nil {
		return nil, owners, fmt.Errorf("persist atomic write rename probe peer binding: %w", err)
	}
	if err := callAtomicWriteRenameProbeFaultHook("checkpoint:setup_peer_bound"); err != nil {
		return nil, owners, err
	}

	manifest := &atomicWriteRenameProbeJournalManifest{
		Schema:       atomicWriteRenameProbeJournalSchema,
		OperationID:  operationID,
		FilesRoot:    filesRootIdentity,
		InternalRoot: internalRootIdentity,
		StagingRoot:  stagingRootIdentity,
		JournalRoot:  journalRootIdentity,
		Paths:        paths,
		Source:       atomicWriteRenameProbeObjectManifest(source),
		Peer:         atomicWriteRenameProbeObjectManifest(peer),
	}
	if err := validateAtomicWriteRenameProbeManifest(*manifest); err != nil {
		return nil, owners, err
	}
	if err := ctx.writeJournalJSON(atomicWriteRenameProbeManifestName, manifest); err != nil {
		return nil, owners, fmt.Errorf("persist atomic write rename probe manifest: %w", err)
	}
	if err := callAtomicWriteRenameProbeFaultHook("checkpoint:manifest_persisted"); err != nil {
		return manifest, owners, err
	}
	if err := ctx.writePhase(manifest, 0); err != nil {
		return manifest, owners, err
	}
	return manifest, owners, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) createBoundObject(
	role string,
	rel string,
	nonce [atomicWriteRenameProbeNonceSize]byte,
) (*atomicWriteRenameProbeFile, error) {
	pendingRel := filepath.Join(
		atomicWriteRenameProbeJournalDir,
		atomicWriteRenameProbePendingName(filepath.Base(rel)),
	)
	file, err := rootio.OpenFileNoFollow(
		ctx.internalRoot,
		pendingRel,
		os.O_RDWR|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	if err := callAtomicWriteRenameProbeFaultHook(
		atomicWriteRenameProbeObjectFaultPoint(role, "after_create"),
	); err != nil {
		_ = file.Close()
		return nil, err
	}
	owner := &atomicWriteRenameProbeFile{
		file:  file,
		nonce: nonce,
		hash:  sha256.Sum256(nonce[:]),
	}
	split := len(owner.nonce) / 2
	written, err := owner.file.Write(owner.nonce[:split])
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if written != split {
		_ = file.Close()
		return nil, io.ErrShortWrite
	}
	if err := callAtomicWriteRenameProbeFaultHook(
		atomicWriteRenameProbeObjectFaultPoint(role, "after_partial_write"),
	); err != nil {
		_ = file.Close()
		return nil, err
	}
	written, err = owner.file.Write(owner.nonce[split:])
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if written != len(owner.nonce)-split {
		_ = file.Close()
		return nil, io.ErrShortWrite
	}
	if err := owner.file.Sync(); err != nil {
		_ = file.Close()
		return nil, err
	}
	pendingSlot := &atomicWriteRenameProbeSlot{root: ctx.internalRoot, rel: pendingRel}
	if err := rebaselineAtomicWriteRenameProbeFile(pendingSlot, owner); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := callAtomicWriteRenameProbeFaultHook(
		atomicWriteRenameProbeObjectFaultPoint(role, "after_file_sync"),
	); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := rootio.RenameLeafNoReplace(ctx.internalRoot, pendingRel, rel); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := ctx.journalDir.Sync(); err != nil {
		_ = file.Close()
		return nil, err
	}
	finalSlot := &atomicWriteRenameProbeSlot{root: ctx.internalRoot, rel: rel}
	if err := rebaselineAtomicWriteRenameProbeFile(finalSlot, owner); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := callAtomicWriteRenameProbeFaultHook(
		atomicWriteRenameProbeObjectFaultPoint(role, "after_publish"),
	); err != nil {
		_ = file.Close()
		return nil, err
	}
	return owner, nil
}

func atomicWriteRenameProbeObjectFaultPoint(role, stage string) string {
	return "object:" + role + ":" + stage
}

func atomicWriteRenameProbeSetupObjectFromNonce(
	nonce [atomicWriteRenameProbeNonceSize]byte,
) atomicWriteRenameProbeSetupObject {
	hash := sha256.Sum256(nonce[:])
	return atomicWriteRenameProbeSetupObject{
		Nonce:  hex.EncodeToString(nonce[:]),
		SHA256: hex.EncodeToString(hash[:]),
	}
}

func atomicWriteRenameProbeRootIdentity(file *os.File) (atomicWriteRenameProbeJournalRoot, error) {
	info, err := file.Stat()
	if err != nil {
		return atomicWriteRenameProbeJournalRoot{}, err
	}
	if !info.IsDir() {
		return atomicWriteRenameProbeJournalRoot{}, errAtomicWriteRenameProbeOwnershipChanged
	}
	persistentIdentity := workspace.PersistentIdentityTokenForFileInfo(info)
	if persistentIdentity == "" {
		return atomicWriteRenameProbeJournalRoot{}, errors.New("directory persistent identity is unavailable")
	}
	return atomicWriteRenameProbeJournalRoot{
		PersistentIdentity: persistentIdentity,
		Mode:               uint32(info.Mode()),
	}, nil
}

func atomicWriteRenameProbeObjectManifest(
	owner *atomicWriteRenameProbeFile,
) atomicWriteRenameProbeJournalObject {
	return atomicWriteRenameProbeJournalObject{
		Nonce:              hex.EncodeToString(owner.nonce[:]),
		SHA256:             hex.EncodeToString(owner.hash[:]),
		PersistentIdentity: owner.evidence.persistentIdentity,
		Mode:               uint32(owner.evidence.mode),
		Size:               owner.evidence.size,
		ModTimeUnixNano:    owner.evidence.modTime.UnixNano(),
	}
}

func (ctx *atomicWriteRenameProbeJournalContext) runOperation(
	manifest *atomicWriteRenameProbeJournalManifest,
) error {
	if err := ctx.moveWithPhases(
		manifest,
		1,
		ctx.internalRoot,
		manifest.Paths.SourceIsolation,
		ctx.internalRoot,
		manifest.Paths.Source,
		ctx.journalDir,
		ctx.stagingDir,
		rootio.RenameLeafBetweenRootsNoReplace,
	); err != nil {
		return fmt.Errorf("move probe source into staging: %w", err)
	}
	if err := ctx.moveWithPhases(
		manifest,
		3,
		ctx.internalRoot,
		manifest.Paths.PeerIsolation,
		ctx.internalRoot,
		manifest.Paths.Peer,
		ctx.journalDir,
		ctx.stagingDir,
		rootio.RenameLeafBetweenRootsNoReplace,
	); err != nil {
		return fmt.Errorf("move probe peer into staging: %w", err)
	}
	if err := ctx.moveWithPhases(
		manifest,
		5,
		ctx.internalRoot,
		manifest.Paths.Peer,
		ctx.filesRoot,
		manifest.Paths.FilesSlot,
		ctx.stagingDir,
		ctx.filesDir,
		ctx.rename,
	); err != nil {
		return fmt.Errorf("internal-to-files no-replace probe: %w", err)
	}
	if err := ctx.rejectReplacementWithPhases(
		manifest,
		7,
		ctx.internalRoot,
		manifest.Paths.Source,
		ctx.filesRoot,
		manifest.Paths.FilesSlot,
		ctx.rename,
	); err != nil {
		return fmt.Errorf("internal-to-files no-replace probe: %w", err)
	}
	if err := ctx.rejectReplacementWithPhases(
		manifest,
		9,
		ctx.filesRoot,
		manifest.Paths.FilesSlot,
		ctx.internalRoot,
		manifest.Paths.Source,
		ctx.rename,
	); err != nil {
		return fmt.Errorf("files-to-internal no-replace probe: %w", err)
	}
	if err := ctx.moveWithPhases(
		manifest,
		11,
		ctx.filesRoot,
		manifest.Paths.FilesSlot,
		ctx.internalRoot,
		manifest.Paths.Peer,
		ctx.filesDir,
		ctx.stagingDir,
		ctx.rename,
	); err != nil {
		return fmt.Errorf("files-to-internal successful no-replace probe: %w", err)
	}
	if err := ctx.moveWithPhases(
		manifest,
		13,
		ctx.internalRoot,
		manifest.Paths.Peer,
		ctx.filesRoot,
		manifest.Paths.FilesSlot,
		ctx.stagingDir,
		ctx.filesDir,
		ctx.rename,
	); err != nil {
		return fmt.Errorf("prepare files-root atomic exchange peer: %w", err)
	}
	if err := ctx.exchangeWithPhases(
		manifest,
		15,
		ctx.internalRoot,
		manifest.Paths.Source,
		ctx.filesRoot,
		manifest.Paths.FilesSlot,
		ctx.stagingDir,
		ctx.filesDir,
	); err != nil {
		return fmt.Errorf("internal-to-files atomic exchange probe: %w", err)
	}
	if err := ctx.exchangeWithPhases(
		manifest,
		17,
		ctx.filesRoot,
		manifest.Paths.FilesSlot,
		ctx.internalRoot,
		manifest.Paths.Source,
		ctx.filesDir,
		ctx.stagingDir,
	); err != nil {
		return fmt.Errorf("files-to-internal atomic exchange probe: %w", err)
	}
	if err := ctx.moveWithPhases(
		manifest,
		19,
		ctx.filesRoot,
		manifest.Paths.FilesSlot,
		ctx.internalRoot,
		manifest.Paths.Peer,
		ctx.filesDir,
		ctx.stagingDir,
		ctx.rename,
	); err != nil {
		return fmt.Errorf("restore probe peer to internal staging: %w", err)
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) moveWithPhases(
	manifest *atomicWriteRenameProbeJournalManifest,
	beforeIndex int,
	sourceRoot *os.Root,
	sourceRel string,
	targetRoot *os.Root,
	targetRel string,
	sourceDir *os.File,
	targetDir *os.File,
	rename atomicWriteRenameFunc,
) error {
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := ctx.writePhase(manifest, beforeIndex); err != nil {
		return err
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := rename(sourceRoot, sourceRel, targetRoot, targetRel); err != nil {
		return err
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := errors.Join(
		syncAtomicWriteRenameProbeDir(sourceDir, "probe rename source"),
		syncAtomicWriteRenameProbeDir(targetDir, "probe rename target"),
	); err != nil {
		return err
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := callAtomicWriteRenameProbeFaultHook("namespace:" + atomicWriteRenameProbePhases[beforeIndex+1].name); err != nil {
		return err
	}
	if err := ctx.verifyExpectedLayout(manifest, atomicWriteRenameProbePhases[beforeIndex].next); err != nil {
		return err
	}
	return ctx.writePhase(manifest, beforeIndex+1)
}

func (ctx *atomicWriteRenameProbeJournalContext) rejectReplacementWithPhases(
	manifest *atomicWriteRenameProbeJournalManifest,
	beforeIndex int,
	sourceRoot *os.Root,
	sourceRel string,
	targetRoot *os.Root,
	targetRel string,
	rename atomicWriteRenameFunc,
) error {
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := ctx.writePhase(manifest, beforeIndex); err != nil {
		return err
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	err := rename(sourceRoot, sourceRel, targetRoot, targetRel)
	if verifyErr := ctx.verifyPrivateDirectoryEntries(); verifyErr != nil {
		return errors.Join(err, verifyErr)
	}
	if !errors.Is(err, os.ErrExist) {
		if err == nil {
			return errors.New("no-replace probe replaced an existing target")
		}
		return err
	}
	if err := ctx.verifyExpectedLayout(manifest, atomicWriteRenameProbePhases[beforeIndex].current); err != nil {
		return err
	}
	return ctx.writePhase(manifest, beforeIndex+1)
}

func (ctx *atomicWriteRenameProbeJournalContext) exchangeWithPhases(
	manifest *atomicWriteRenameProbeJournalManifest,
	beforeIndex int,
	firstRoot *os.Root,
	firstRel string,
	secondRoot *os.Root,
	secondRel string,
	firstDir *os.File,
	secondDir *os.File,
) error {
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := ctx.writePhase(manifest, beforeIndex); err != nil {
		return err
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := ctx.exchange(firstRoot, firstRel, secondRoot, secondRel); err != nil {
		return err
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := errors.Join(
		syncAtomicWriteRenameProbeDir(firstDir, "probe exchange first parent"),
		syncAtomicWriteRenameProbeDir(secondDir, "probe exchange second parent"),
	); err != nil {
		return err
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := callAtomicWriteRenameProbeFaultHook("namespace:" + atomicWriteRenameProbePhases[beforeIndex+1].name); err != nil {
		return err
	}
	if err := ctx.verifyExpectedLayout(manifest, atomicWriteRenameProbePhases[beforeIndex].next); err != nil {
		return err
	}
	return ctx.writePhase(manifest, beforeIndex+1)
}

func (ctx *atomicWriteRenameProbeJournalContext) writePhase(
	manifest *atomicWriteRenameProbeJournalManifest,
	index int,
) error {
	if index < 0 || index >= len(atomicWriteRenameProbePhases) {
		return errors.New("atomic write rename probe phase index is invalid")
	}
	record := atomicWriteRenameProbePhaseRecord{
		Schema:      atomicWriteRenameProbeJournalSchema,
		OperationID: manifest.OperationID,
		Index:       index,
		Phase:       atomicWriteRenameProbePhases[index].name,
	}
	if err := ctx.writeJournalJSON(atomicWriteRenameProbePhaseName(index), record); err != nil {
		return fmt.Errorf("persist atomic write rename probe phase %s: %w", record.Phase, err)
	}
	return callAtomicWriteRenameProbeFaultHook("checkpoint:" + record.Phase)
}

func atomicWriteRenameProbePhaseName(index int) string {
	return fmt.Sprintf("phase-%03d.json", index)
}

func atomicWriteRenameProbeRecoveryPhaseName(index int) string {
	return fmt.Sprintf("recovery-phase-%03d.json", index)
}

func callAtomicWriteRenameProbeFaultHook(point string) error {
	if err := atomicWriteRenameProbeFaultHook(point); err != nil {
		return errors.Join(errAtomicWriteRenameProbeCrashInjected, err)
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) recoverExistingOperation() error {
	if err := ctx.recoverPendingJournalRecord(); err != nil {
		return err
	}
	if err := ctx.recoverPendingProbeObject(); err != nil {
		return err
	}
	entries, err := ctx.readJournalDir()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if _, ok := entries[atomicWriteRenameProbeCleanupName]; ok {
		return ctx.finishJournalCleanup(entries)
	}
	if _, hasSetup := entries[atomicWriteRenameProbeSetupName]; hasSetup {
		if _, hasManifest := entries[atomicWriteRenameProbeManifestName]; !hasManifest {
			return ctx.recoverIncompleteSetup(entries)
		}
	}

	manifest, lastPhase, recovery, lastRecoveryPhase, err := ctx.readActiveJournal(entries)
	if err != nil {
		return err
	}
	if err := ctx.validateRootBindings(manifest); err != nil {
		return err
	}
	observed, err := ctx.inspectLayout(manifest)
	if err != nil {
		return err
	}

	if recovery == nil {
		if err := validateAtomicWriteRenameProbePhaseLayout(lastPhase, observed); err != nil {
			return err
		}
		recovery = &atomicWriteRenameProbeRecoveryRecord{
			Schema:      atomicWriteRenameProbeJournalSchema,
			OperationID: manifest.OperationID,
			SourceFrom:  observed.source,
			PeerFrom:    observed.peer,
		}
		if err := validateAtomicWriteRenameProbeRecoveryRecord(manifest, *recovery); err != nil {
			return err
		}
		if err := ctx.writeJournalJSON(atomicWriteRenameProbeRecoveryName, recovery); err != nil {
			return fmt.Errorf("persist atomic write rename probe recovery start: %w", err)
		}
		if err := callAtomicWriteRenameProbeFaultHook("checkpoint:recovery_started"); err != nil {
			return err
		}
		lastRecoveryPhase = 0
	} else if err := validateAtomicWriteRenameProbeRecoveryLayout(*recovery, lastRecoveryPhase, observed); err != nil {
		return err
	}

	if lastRecoveryPhase < 1 {
		observed, err = ctx.inspectLayout(manifest)
		if err != nil {
			return err
		}
		if observed.source != atomicWriteRenameProbeLocationMissing &&
			observed.source != atomicWriteRenameProbeLocationSourceIsolation {
			if err := ctx.moveObservedObjectToIsolation(
				manifest,
				observed.source,
				manifest.Paths.SourceIsolation,
				atomicWriteRenameProbeLocationSourceIsolation,
			); err != nil {
				return fmt.Errorf("isolate journaled probe source: %w", err)
			}
		}
		if err := ctx.writeRecoveryPhase(manifest, 1, "source_isolated"); err != nil {
			return err
		}
		lastRecoveryPhase = 1
	}
	if lastRecoveryPhase < 2 {
		if err := ctx.removeIsolatedObject(manifest, true); err != nil {
			return fmt.Errorf("remove journaled probe source: %w", err)
		}
		if err := ctx.writeRecoveryPhase(manifest, 2, "source_removed"); err != nil {
			return err
		}
		lastRecoveryPhase = 2
	}
	if lastRecoveryPhase < 3 {
		observed, err = ctx.inspectLayout(manifest)
		if err != nil {
			return err
		}
		if observed.peer != atomicWriteRenameProbeLocationMissing &&
			observed.peer != atomicWriteRenameProbeLocationPeerIsolation {
			if err := ctx.moveObservedObjectToIsolation(
				manifest,
				observed.peer,
				manifest.Paths.PeerIsolation,
				atomicWriteRenameProbeLocationPeerIsolation,
			); err != nil {
				return fmt.Errorf("isolate journaled probe peer: %w", err)
			}
		}
		if err := ctx.writeRecoveryPhase(manifest, 3, "peer_isolated"); err != nil {
			return err
		}
		lastRecoveryPhase = 3
	}
	if lastRecoveryPhase < 4 {
		if err := ctx.removeIsolatedObject(manifest, false); err != nil {
			return fmt.Errorf("remove journaled probe peer: %w", err)
		}
		if err := ctx.writeRecoveryPhase(manifest, 4, "peer_removed"); err != nil {
			return err
		}
	}
	observed, err = ctx.inspectLayout(manifest)
	if err != nil {
		return err
	}
	if observed.source != atomicWriteRenameProbeLocationMissing ||
		observed.peer != atomicWriteRenameProbeLocationMissing {
		return errAtomicWriteRenameProbeOwnershipChanged
	}

	cleanup := atomicWriteRenameProbeCleanupRecord{
		Schema:   atomicWriteRenameProbeJournalSchema,
		Manifest: manifest,
	}
	if err := ctx.writeJournalJSON(atomicWriteRenameProbeCleanupName, cleanup); err != nil {
		return fmt.Errorf("persist atomic write rename probe cleanup marker: %w", err)
	}
	if err := callAtomicWriteRenameProbeFaultHook("checkpoint:cleanup"); err != nil {
		return err
	}
	entries, err = ctx.readJournalDir()
	if err != nil {
		return err
	}
	return ctx.finishJournalCleanup(entries)
}

func (ctx *atomicWriteRenameProbeJournalContext) recoverPendingProbeObject() error {
	entries, err := ctx.readJournalDir()
	if err != nil {
		return err
	}
	if err := validateAtomicWriteRenameProbeJournalEntryNames(entries); err != nil {
		return err
	}
	type pendingObjectSpec struct {
		role        string
		pendingName string
		targetName  string
		bindingName string
	}
	specs := []pendingObjectSpec{
		{
			role:        "source",
			pendingName: atomicWriteRenameProbePendingName(atomicWriteRenameProbeSourceIsolation),
			targetName:  atomicWriteRenameProbeSourceIsolation,
			bindingName: atomicWriteRenameProbeSourceBindingName,
		},
		{
			role:        "peer",
			pendingName: atomicWriteRenameProbePendingName(atomicWriteRenameProbePeerIsolation),
			targetName:  atomicWriteRenameProbePeerIsolation,
			bindingName: atomicWriteRenameProbePeerBindingName,
		},
	}
	var selected *pendingObjectSpec
	for index := range specs {
		if _, exists := entries[specs[index].pendingName]; !exists {
			continue
		}
		if selected != nil {
			return errors.New("atomic write rename probe journal contains multiple pending objects")
		}
		selected = &specs[index]
	}
	if selected == nil {
		return nil
	}
	if _, exists := entries[selected.targetName]; exists {
		return errors.New("atomic write rename probe journal contains both pending and published probe objects")
	}
	if _, exists := entries[selected.bindingName]; exists {
		return errors.New("atomic write rename probe journal contains a binding for a pending probe object")
	}
	if _, exists := entries[atomicWriteRenameProbeSetupName]; !exists {
		return errors.New("atomic write rename probe pending object lacks setup intent")
	}

	var setup atomicWriteRenameProbeSetupRecord
	if err := ctx.readJournalJSON(atomicWriteRenameProbeSetupName, &setup); err != nil {
		return fmt.Errorf("read setup intent for pending probe object: %w", err)
	}
	if err := validateAtomicWriteRenameProbeSetupRecord(setup); err != nil {
		return err
	}
	if err := ctx.validateSetupRootBindings(setup); err != nil {
		return err
	}
	if _, hasManifest := entries[atomicWriteRenameProbeManifestName]; !hasManifest {
		if err := validateAtomicWriteRenameProbeIncompleteSetupPrefix(entries); err != nil {
			return err
		}
	}
	intent := setup.Source
	targetRel := setup.Paths.SourceIsolation
	if selected.role == "peer" {
		intent = setup.Peer
		targetRel = setup.Paths.PeerIsolation
	}
	if filepath.Base(targetRel) != selected.targetName {
		return errors.New("atomic write rename probe pending object target does not match setup intent")
	}
	pendingRel := filepath.Join(atomicWriteRenameProbeJournalDir, selected.pendingName)
	file, err := rootio.OpenRegularFileNoFollow(ctx.internalRoot, pendingRel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	defer file.Close()
	lstatInfo, err := ctx.internalRoot.Lstat(pendingRel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(lstatInfo, openedInfo) {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	complete, partial, err := atomicWriteRenameProbePendingObjectState(file, openedInfo, intent)
	if err != nil {
		return err
	}
	if !complete && !partial {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	if partial {
		if err := atomicWriteRenameProbeBeforePendingObjectAction(selected.role, "remove"); err != nil {
			return err
		}
		if err := ctx.verifyCurrentPendingProbeObject(
			pendingRel,
			file,
			openedInfo,
			intent,
			false,
		); err != nil {
			return err
		}
		return ctx.removeJournalLeafChecked(
			selected.pendingName,
			file,
			openedInfo,
			"pending_object:"+selected.role,
		)
	}

	if err := atomicWriteRenameProbeBeforePendingObjectAction(selected.role, "publish"); err != nil {
		return err
	}
	if err := ctx.verifyCurrentPendingProbeObject(
		pendingRel,
		file,
		openedInfo,
		intent,
		true,
	); err != nil {
		return err
	}
	if err := rootio.RenameLeafNoReplace(ctx.internalRoot, pendingRel, targetRel); err != nil {
		return err
	}
	if err := ctx.journalDir.Sync(); err != nil {
		return err
	}
	owner, err := atomicWriteRenameProbeOwnerFromSetupIntent(file, intent)
	if err != nil {
		return err
	}
	slot := &atomicWriteRenameProbeSlot{root: ctx.internalRoot, rel: targetRel}
	if err := rebaselineAtomicWriteRenameProbeFile(slot, owner); err != nil {
		return err
	}
	return nil
}

func atomicWriteRenameProbePendingObjectState(
	file *os.File,
	info os.FileInfo,
	intent atomicWriteRenameProbeSetupObject,
) (complete bool, partial bool, resultErr error) {
	if file == nil || info == nil || !info.Mode().IsRegular() || info.Mode() != 0o600 ||
		info.Size() < 0 || info.Size() > atomicWriteRenameProbeNonceSize {
		return false, false, nil
	}
	nonce, err := hex.DecodeString(intent.Nonce)
	if err != nil {
		return false, false, err
	}
	content := make([]byte, info.Size()+1)
	read, readErr := file.ReadAt(content, 0)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, false, readErr
	}
	if int64(read) != info.Size() || !bytes.Equal(content[:read], nonce[:read]) {
		return false, false, nil
	}
	if read < len(nonce) {
		return false, true, nil
	}
	sum := sha256.Sum256(content[:read])
	return hex.EncodeToString(sum[:]) == intent.SHA256, false, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) verifyCurrentPendingProbeObject(
	rel string,
	held *os.File,
	expectedInfo os.FileInfo,
	intent atomicWriteRenameProbeSetupObject,
	requireComplete bool,
) error {
	current, err := rootio.OpenRegularFileNoFollow(ctx.internalRoot, rel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	defer current.Close()
	lstatInfo, err := ctx.internalRoot.Lstat(rel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	currentInfo, err := current.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	heldInfo, err := held.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if expectedInfo == nil || !os.SameFile(expectedInfo, lstatInfo) ||
		!os.SameFile(lstatInfo, currentInfo) || !os.SameFile(currentInfo, heldInfo) ||
		expectedInfo.Mode() != currentInfo.Mode() ||
		expectedInfo.Size() != currentInfo.Size() ||
		!expectedInfo.ModTime().Equal(currentInfo.ModTime()) ||
		workspace.DeleteIdentityTokenForFileInfo(expectedInfo) !=
			workspace.DeleteIdentityTokenForFileInfo(currentInfo) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	complete, partial, err := atomicWriteRenameProbePendingObjectState(current, currentInfo, intent)
	if err != nil {
		return err
	}
	if (requireComplete && !complete) || (!requireComplete && !partial) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return nil
}

func atomicWriteRenameProbeOwnerFromSetupIntent(
	file *os.File,
	intent atomicWriteRenameProbeSetupObject,
) (*atomicWriteRenameProbeFile, error) {
	nonce, err := hex.DecodeString(intent.Nonce)
	if err != nil || len(nonce) != atomicWriteRenameProbeNonceSize {
		return nil, errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	owner := &atomicWriteRenameProbeFile{file: file}
	copy(owner.nonce[:], nonce)
	owner.hash = sha256.Sum256(owner.nonce[:])
	return owner, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) recoverPendingJournalRecord() error {
	entries, err := ctx.readJournalDir()
	if err != nil {
		return err
	}
	if err := validateAtomicWriteRenameProbeJournalEntryNames(entries); err != nil {
		return err
	}
	var pendingName string
	var targetName string
	for name := range entries {
		target, ok := atomicWriteRenameProbePendingTarget(name)
		if !ok {
			continue
		}
		if pendingName != "" {
			return errors.New("atomic write rename probe journal contains multiple pending records")
		}
		pendingName = name
		targetName = target
	}
	if pendingName == "" {
		return nil
	}
	if _, exists := entries[targetName]; exists {
		return errors.New("atomic write rename probe journal contains both pending and published records")
	}
	if err := validateAtomicWriteRenameProbePendingRecordPlacement(
		entries,
		pendingName,
		targetName,
	); err != nil {
		return err
	}
	if err := ctx.validatePendingJournalRecord(entries, pendingName, targetName); err != nil {
		if errors.Is(err, errAtomicWriteRenameProbePendingRecordIncomplete) {
			if removeErr := ctx.removeJournalMetadataFile(pendingName); removeErr != nil {
				return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, removeErr)
			}
			return nil
		}
		return fmt.Errorf("validate pending atomic write rename probe record: %w", err)
	}
	pendingEvidence, ok := ctx.validatedJournalFile[pendingName]
	if !ok {
		return fmt.Errorf(
			"pending atomic write rename probe record %q lacks validated evidence: %w",
			pendingName,
			errAtomicWriteRenameProbeOwnershipChanged,
		)
	}
	if _, exists := ctx.validatedJournalFile[targetName]; exists {
		return fmt.Errorf(
			"pending atomic write rename probe target %q has stale evidence: %w",
			targetName,
			errAtomicWriteRenameProbeOwnershipChanged,
		)
	}
	if err := rootio.RenameLeafNoReplace(
		ctx.internalRoot,
		filepath.Join(atomicWriteRenameProbeJournalDir, pendingName),
		filepath.Join(atomicWriteRenameProbeJournalDir, targetName),
	); err != nil {
		return fmt.Errorf("publish pending atomic write rename probe record: %w", err)
	}
	if err := ctx.journalDir.Sync(); err != nil {
		return fmt.Errorf("sync published atomic write rename probe record: %w", err)
	}
	targetFile, _, _, targetEvidence, err := ctx.openStableJournalFile(targetName)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if closeErr := targetFile.Close(); closeErr != nil {
		return closeErr
	}
	if !atomicWriteRenameProbeJournalEvidenceMatchesRename(pendingEvidence, targetEvidence) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	delete(ctx.validatedJournalFile, pendingName)
	ctx.validatedJournalFile[targetName] = targetEvidence
	return nil
}

func atomicWriteRenameProbeJournalEvidenceMatchesRename(
	before atomicWriteRenameProbeJournalFileEvidence,
	after atomicWriteRenameProbeJournalFileEvidence,
) bool {
	return before.file.persistentIdentity == after.file.persistentIdentity &&
		before.file.mode == after.file.mode &&
		before.file.size == after.file.size &&
		before.file.modTime.Equal(after.file.modTime) &&
		before.hash == after.hash
}

func atomicWriteRenameProbePendingTarget(name string) (string, bool) {
	if !strings.HasPrefix(name, atomicWriteRenameProbePendingPrefix) {
		return "", false
	}
	target := strings.TrimPrefix(name, atomicWriteRenameProbePendingPrefix)
	switch target {
	case atomicWriteRenameProbeSetupName,
		atomicWriteRenameProbeSourceBindingName,
		atomicWriteRenameProbePeerBindingName,
		atomicWriteRenameProbeManifestName,
		atomicWriteRenameProbeRecoveryName,
		atomicWriteRenameProbeCleanupName:
		return target, true
	}
	for index := range atomicWriteRenameProbePhases {
		if target == atomicWriteRenameProbePhaseName(index) {
			return target, true
		}
	}
	for index := 1; index <= 4; index++ {
		if target == atomicWriteRenameProbeRecoveryPhaseName(index) {
			return target, true
		}
	}
	return "", false
}

func validateAtomicWriteRenameProbePendingRecordPlacement(
	entries map[string]os.DirEntry,
	pendingName string,
	targetName string,
) error {
	simulated := make(map[string]struct{}, len(entries))
	for name := range entries {
		if strings.HasPrefix(name, atomicWriteRenameProbePendingPrefix) {
			if name != pendingName {
				return fmt.Errorf(
					"atomic write rename probe journal contains concurrent pending entry %q",
					name,
				)
			}
			continue
		}
		simulated[name] = struct{}{}
	}
	simulated[targetName] = struct{}{}

	setupOrder := []string{
		atomicWriteRenameProbeSetupName,
		atomicWriteRenameProbeSourceIsolation,
		atomicWriteRenameProbeSourceBindingName,
		atomicWriteRenameProbePeerIsolation,
		atomicWriteRenameProbePeerBindingName,
		atomicWriteRenameProbeManifestName,
	}
	setupIndex := -1
	for index, name := range setupOrder {
		if name == targetName {
			setupIndex = index
			break
		}
	}
	if setupIndex >= 0 {
		if err := validateAtomicWriteRenameProbeExactNamePrefix(
			simulated,
			setupOrder,
			setupIndex,
		); err != nil {
			return fmt.Errorf(
				"atomic write rename probe pending %q is out of order: %w",
				targetName,
				err,
			)
		}
		return nil
	}

	for _, name := range []string{
		atomicWriteRenameProbeSetupName,
		atomicWriteRenameProbeSourceBindingName,
		atomicWriteRenameProbePeerBindingName,
		atomicWriteRenameProbeManifestName,
	} {
		if _, exists := simulated[name]; !exists {
			return fmt.Errorf(
				"atomic write rename probe pending %q lacks setup predecessor %q",
				targetName,
				name,
			)
		}
		delete(simulated, name)
	}
	// Once the manifest is durable, the two probe objects move among their
	// isolation slots, write staging, and the files root. Their current
	// journal-directory names are layout evidence, not checkpoint-order
	// metadata, and are validated separately against the surrounding phases.
	delete(simulated, atomicWriteRenameProbeSourceIsolation)
	delete(simulated, atomicWriteRenameProbePeerIsolation)

	phaseIndex := -1
	for index := range atomicWriteRenameProbePhases {
		name := atomicWriteRenameProbePhaseName(index)
		if name == targetName {
			phaseIndex = index
			break
		}
	}
	if phaseIndex >= 0 {
		if err := validateAtomicWriteRenameProbeExactNamePrefix(
			simulated,
			atomicWriteRenameProbePhaseNames(),
			phaseIndex,
		); err != nil {
			return fmt.Errorf(
				"atomic write rename probe pending phase %q is out of order: %w",
				targetName,
				err,
			)
		}
		return nil
	}

	lastPhase, err := consumeAtomicWriteRenameProbePhasePrefix(simulated)
	if err != nil {
		return err
	}
	for index := 0; index <= lastPhase; index++ {
		delete(simulated, atomicWriteRenameProbePhaseName(index))
	}

	if targetName == atomicWriteRenameProbeRecoveryName {
		if len(simulated) != 1 {
			return fmt.Errorf(
				"atomic write rename probe pending recovery has unexpected predecessors: %v",
				sortedAtomicWriteRenameProbeNames(simulated),
			)
		}
		if _, exists := simulated[targetName]; !exists {
			return errors.New("atomic write rename probe pending recovery placement is invalid")
		}
		return nil
	}
	if _, exists := simulated[atomicWriteRenameProbeRecoveryName]; !exists {
		return fmt.Errorf(
			"atomic write rename probe pending %q lacks recovery predecessor",
			targetName,
		)
	}
	delete(simulated, atomicWriteRenameProbeRecoveryName)

	recoveryNames := make([]string, 4)
	recoveryIndex := -1
	for index := 1; index <= 4; index++ {
		recoveryNames[index-1] = atomicWriteRenameProbeRecoveryPhaseName(index)
		if recoveryNames[index-1] == targetName {
			recoveryIndex = index - 1
		}
	}
	if recoveryIndex >= 0 {
		if err := validateAtomicWriteRenameProbeExactNamePrefix(
			simulated,
			recoveryNames,
			recoveryIndex,
		); err != nil {
			return fmt.Errorf(
				"atomic write rename probe pending recovery phase %q is out of order: %w",
				targetName,
				err,
			)
		}
		return nil
	}
	if targetName != atomicWriteRenameProbeCleanupName {
		return fmt.Errorf("atomic write rename probe pending target %q is invalid", targetName)
	}
	for _, name := range recoveryNames {
		if _, exists := simulated[name]; !exists {
			return fmt.Errorf(
				"atomic write rename probe pending cleanup lacks predecessor %q",
				name,
			)
		}
		delete(simulated, name)
	}
	if len(simulated) != 1 {
		return fmt.Errorf(
			"atomic write rename probe pending cleanup has unexpected predecessors: %v",
			sortedAtomicWriteRenameProbeNames(simulated),
		)
	}
	if _, exists := simulated[targetName]; !exists {
		return errors.New("atomic write rename probe pending cleanup placement is invalid")
	}
	return nil
}

func validateAtomicWriteRenameProbeExactNamePrefix(
	entries map[string]struct{},
	order []string,
	lastIndex int,
) error {
	if lastIndex < 0 || lastIndex >= len(order) {
		return errors.New("atomic write rename probe pending prefix index is invalid")
	}
	if len(entries) != lastIndex+1 {
		return fmt.Errorf(
			"expected %d entries, found %d (%v)",
			lastIndex+1,
			len(entries),
			sortedAtomicWriteRenameProbeNames(entries),
		)
	}
	for index := 0; index <= lastIndex; index++ {
		if _, exists := entries[order[index]]; !exists {
			return fmt.Errorf("missing predecessor %q", order[index])
		}
	}
	return nil
}

func atomicWriteRenameProbePhaseNames() []string {
	names := make([]string, len(atomicWriteRenameProbePhases))
	for index := range atomicWriteRenameProbePhases {
		names[index] = atomicWriteRenameProbePhaseName(index)
	}
	return names
}

func consumeAtomicWriteRenameProbePhasePrefix(entries map[string]struct{}) (int, error) {
	lastPhase := -1
	gap := false
	for index := range atomicWriteRenameProbePhases {
		_, exists := entries[atomicWriteRenameProbePhaseName(index)]
		if !exists {
			gap = true
			continue
		}
		if gap {
			return -1, fmt.Errorf(
				"atomic write rename probe phase %d lacks its predecessor",
				index,
			)
		}
		lastPhase = index
	}
	return lastPhase, nil
}

func sortedAtomicWriteRenameProbeNames(entries map[string]struct{}) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (ctx *atomicWriteRenameProbeJournalContext) validatePendingJournalRecord(
	entries map[string]os.DirEntry,
	pendingName string,
	targetName string,
) error {
	switch targetName {
	case atomicWriteRenameProbeSetupName:
		var setup atomicWriteRenameProbeSetupRecord
		if err := ctx.readJournalJSON(pendingName, &setup); err != nil {
			return err
		}
		if err := validateAtomicWriteRenameProbeSetupRecord(setup); err != nil {
			return err
		}
		return ctx.validateSetupRootBindings(setup)
	case atomicWriteRenameProbeSourceBindingName, atomicWriteRenameProbePeerBindingName:
		var setup atomicWriteRenameProbeSetupRecord
		if err := ctx.readJournalJSON(atomicWriteRenameProbeSetupName, &setup); err != nil {
			return err
		}
		if err := validateAtomicWriteRenameProbeSetupRecord(setup); err != nil {
			return err
		}
		var binding atomicWriteRenameProbeBindingRecord
		if err := ctx.readJournalJSON(pendingName, &binding); err != nil {
			return err
		}
		role := "source"
		if targetName == atomicWriteRenameProbePeerBindingName {
			role = "peer"
		}
		return validateAtomicWriteRenameProbeBindingRecord(setup, binding, role)
	case atomicWriteRenameProbeManifestName:
		var manifest atomicWriteRenameProbeJournalManifest
		if err := ctx.readJournalJSON(pendingName, &manifest); err != nil {
			return err
		}
		if err := validateAtomicWriteRenameProbeManifest(manifest); err != nil {
			return err
		}
		return ctx.validateActiveSetupMetadata(entries, manifest)
	case atomicWriteRenameProbeRecoveryName:
		manifest, err := ctx.readPublishedManifest()
		if err != nil {
			return err
		}
		var recovery atomicWriteRenameProbeRecoveryRecord
		if err := ctx.readJournalJSON(pendingName, &recovery); err != nil {
			return err
		}
		return validateAtomicWriteRenameProbeRecoveryRecord(manifest, recovery)
	case atomicWriteRenameProbeCleanupName:
		var cleanup atomicWriteRenameProbeCleanupRecord
		if err := ctx.readJournalJSON(pendingName, &cleanup); err != nil {
			return err
		}
		if cleanup.Schema != atomicWriteRenameProbeJournalSchema {
			return errors.New("pending atomic write rename probe cleanup schema is invalid")
		}
		if err := validateAtomicWriteRenameProbeManifest(cleanup.Manifest); err != nil {
			return err
		}
		return ctx.validateRootBindings(cleanup.Manifest)
	}

	manifest, err := ctx.readPublishedManifest()
	if err != nil {
		return err
	}
	var phase atomicWriteRenameProbePhaseRecord
	if err := ctx.readJournalJSON(pendingName, &phase); err != nil {
		return err
	}
	for index, definition := range atomicWriteRenameProbePhases {
		if targetName != atomicWriteRenameProbePhaseName(index) {
			continue
		}
		if phase.Schema != atomicWriteRenameProbeJournalSchema ||
			phase.OperationID != manifest.OperationID ||
			phase.Index != index ||
			phase.Phase != definition.name {
			return errors.New("pending atomic write rename probe phase is invalid")
		}
		return nil
	}
	for index, name := range []string{"", "source_isolated", "source_removed", "peer_isolated", "peer_removed"} {
		if index == 0 || targetName != atomicWriteRenameProbeRecoveryPhaseName(index) {
			continue
		}
		if phase.Schema != atomicWriteRenameProbeJournalSchema ||
			phase.OperationID != manifest.OperationID ||
			phase.Index != index ||
			phase.Phase != name {
			return errors.New("pending atomic write rename probe recovery phase is invalid")
		}
		return nil
	}
	return errors.New("pending atomic write rename probe target is invalid")
}

func (ctx *atomicWriteRenameProbeJournalContext) readPublishedManifest() (
	atomicWriteRenameProbeJournalManifest,
	error,
) {
	var manifest atomicWriteRenameProbeJournalManifest
	if err := ctx.readJournalJSON(atomicWriteRenameProbeManifestName, &manifest); err != nil {
		return manifest, err
	}
	if err := validateAtomicWriteRenameProbeManifest(manifest); err != nil {
		return manifest, err
	}
	if err := ctx.validateRootBindings(manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) recoverIncompleteSetup(
	entries map[string]os.DirEntry,
) error {
	if err := validateAtomicWriteRenameProbeJournalEntryNames(entries); err != nil {
		return err
	}
	for _, forbidden := range []string{
		atomicWriteRenameProbeManifestName,
		atomicWriteRenameProbeRecoveryName,
		atomicWriteRenameProbeCleanupName,
	} {
		if _, exists := entries[forbidden]; exists {
			return errors.New("incomplete atomic write rename probe setup contains active-operation metadata")
		}
	}
	for index := range atomicWriteRenameProbePhases {
		if _, exists := entries[atomicWriteRenameProbePhaseName(index)]; exists {
			return errors.New("incomplete atomic write rename probe setup contains a phase checkpoint")
		}
	}
	for index := 1; index <= 4; index++ {
		if _, exists := entries[atomicWriteRenameProbeRecoveryPhaseName(index)]; exists {
			return errors.New("incomplete atomic write rename probe setup contains a recovery checkpoint")
		}
	}

	var setup atomicWriteRenameProbeSetupRecord
	if err := ctx.readJournalJSON(atomicWriteRenameProbeSetupName, &setup); err != nil {
		return fmt.Errorf("read atomic write rename probe setup intent: %w", err)
	}
	if err := validateAtomicWriteRenameProbeSetupRecord(setup); err != nil {
		return err
	}
	if err := ctx.validateSetupRootBindings(setup); err != nil {
		return err
	}
	if err := validateAtomicWriteRenameProbeIncompleteSetupPrefix(entries); err != nil {
		return err
	}
	for _, location := range []struct {
		root *os.Root
		rel  string
	}{
		{ctx.internalRoot, setup.Paths.Source},
		{ctx.internalRoot, setup.Paths.Peer},
		{ctx.filesRoot, setup.Paths.FilesSlot},
	} {
		if _, err := location.root.Lstat(location.rel); err == nil {
			return fmt.Errorf(
				"incomplete atomic write rename probe setup has an object outside isolation: %w",
				errAtomicWriteRenameProbeOwnershipChanged,
			)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	sourceExpected, err := ctx.readIncompleteSetupObject(
		entries,
		setup,
		"source",
		setup.Paths.SourceIsolation,
		setup.Source,
		atomicWriteRenameProbeSourceBindingName,
	)
	if err != nil {
		return err
	}
	peerExpected, err := ctx.readIncompleteSetupObject(
		entries,
		setup,
		"peer",
		setup.Paths.PeerIsolation,
		setup.Peer,
		atomicWriteRenameProbePeerBindingName,
	)
	if err != nil {
		return err
	}

	// Validate every owned object and binding before changing the journal, then
	// remove the durable setup prefix in reverse order. Every crash boundary
	// must leave another valid prefix for the next recovery attempt.
	if _, exists := entries[atomicWriteRenameProbePeerBindingName]; exists {
		if err := ctx.removeJournalMetadataFile(atomicWriteRenameProbePeerBindingName); err != nil {
			return err
		}
		if err := callAtomicWriteRenameProbeFaultHook(
			"checkpoint:incomplete_setup_peer_binding_removed",
		); err != nil {
			return err
		}
	}
	if peerExpected != nil {
		if err := ctx.removeJournalObjectChecked(
			setup.Paths.PeerIsolation,
			*peerExpected,
			"setup_peer",
		); err != nil {
			return err
		}
		if err := callAtomicWriteRenameProbeFaultHook(
			"checkpoint:incomplete_setup_peer_object_removed",
		); err != nil {
			return err
		}
	}
	if _, exists := entries[atomicWriteRenameProbeSourceBindingName]; exists {
		if err := ctx.removeJournalMetadataFile(atomicWriteRenameProbeSourceBindingName); err != nil {
			return err
		}
		if err := callAtomicWriteRenameProbeFaultHook(
			"checkpoint:incomplete_setup_source_binding_removed",
		); err != nil {
			return err
		}
	}
	if sourceExpected != nil {
		if err := ctx.removeJournalObjectChecked(
			setup.Paths.SourceIsolation,
			*sourceExpected,
			"setup_source",
		); err != nil {
			return err
		}
		if err := callAtomicWriteRenameProbeFaultHook(
			"checkpoint:incomplete_setup_source_object_removed",
		); err != nil {
			return err
		}
	}
	if _, exists := entries[atomicWriteRenameProbeSetupName]; exists {
		if err := ctx.removeJournalMetadataFile(atomicWriteRenameProbeSetupName); err != nil {
			return err
		}
		if err := callAtomicWriteRenameProbeFaultHook(
			"checkpoint:incomplete_setup_intent_removed",
		); err != nil {
			return err
		}
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) readIncompleteSetupObject(
	entries map[string]os.DirEntry,
	setup atomicWriteRenameProbeSetupRecord,
	role string,
	rel string,
	intent atomicWriteRenameProbeSetupObject,
	bindingName string,
) (*atomicWriteRenameProbeJournalObject, error) {
	var binding *atomicWriteRenameProbeBindingRecord
	if _, exists := entries[bindingName]; exists {
		var record atomicWriteRenameProbeBindingRecord
		if err := ctx.readJournalJSON(bindingName, &record); err != nil {
			return nil, fmt.Errorf("read atomic write rename probe %s binding: %w", role, err)
		}
		if err := validateAtomicWriteRenameProbeBindingRecord(setup, record, role); err != nil {
			return nil, err
		}
		binding = &record
	}

	info, err := ctx.internalRoot.Lstat(rel)
	if errors.Is(err, os.ErrNotExist) {
		if binding != nil {
			return nil, errAtomicWriteRenameProbeOwnershipChanged
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errAtomicWriteRenameProbeOwnershipChanged
	}
	if binding != nil {
		if err := ctx.verifyObjectAt(ctx.internalRoot, rel, binding.Object); err != nil {
			return nil, err
		}
		expected := binding.Object
		return &expected, nil
	}

	file, err := rootio.OpenRegularFileNoFollow(ctx.internalRoot, rel)
	if err != nil {
		return nil, errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	matches, err := atomicWriteRenameProbeSetupObjectMatches(file, openedInfo, intent)
	if err != nil {
		return nil, err
	}
	if !matches {
		return nil, errAtomicWriteRenameProbeOwnershipChanged
	}
	persistentIdentity := workspace.PersistentIdentityTokenForFileInfo(openedInfo)
	if persistentIdentity == "" {
		return nil, errAtomicWriteRenameProbeOwnershipChanged
	}
	expected := atomicWriteRenameProbeJournalObject{
		Nonce:              intent.Nonce,
		SHA256:             intent.SHA256,
		PersistentIdentity: persistentIdentity,
		Mode:               uint32(openedInfo.Mode()),
		Size:               openedInfo.Size(),
		ModTimeUnixNano:    openedInfo.ModTime().UnixNano(),
	}
	return &expected, nil
}

func validateAtomicWriteRenameProbeIncompleteSetupPrefix(
	entries map[string]os.DirEntry,
) error {
	sourcePending := entries[atomicWriteRenameProbePendingName(
		atomicWriteRenameProbeSourceIsolation,
	)] != nil
	sourceObject := entries[atomicWriteRenameProbeSourceIsolation] != nil
	sourceBinding := entries[atomicWriteRenameProbeSourceBindingName] != nil
	peerPending := entries[atomicWriteRenameProbePendingName(
		atomicWriteRenameProbePeerIsolation,
	)] != nil
	peerObject := entries[atomicWriteRenameProbePeerIsolation] != nil
	peerBinding := entries[atomicWriteRenameProbePeerBindingName] != nil

	invalid := func(reason string) error {
		return fmt.Errorf(
			"atomic write rename probe incomplete setup is out of order (%s): %w",
			reason,
			errAtomicWriteRenameProbeOwnershipChanged,
		)
	}
	if sourcePending &&
		(sourceObject || sourceBinding || peerPending || peerObject || peerBinding) {
		return invalid("source pending object has later setup state")
	}
	if sourceBinding && !sourceObject {
		return invalid("source binding lacks its published object")
	}
	if !sourceBinding && (peerPending || peerObject || peerBinding) {
		return invalid("peer state lacks the source binding predecessor")
	}
	if peerPending && (peerObject || peerBinding) {
		return invalid("peer pending object has later peer state")
	}
	if peerBinding && !peerObject {
		return invalid("peer binding lacks its published object")
	}
	return nil
}

func atomicWriteRenameProbeSetupObjectMatches(
	file *os.File,
	info os.FileInfo,
	expected atomicWriteRenameProbeSetupObject,
) (bool, error) {
	if file == nil || info == nil || !info.Mode().IsRegular() ||
		info.Mode() != 0o600 || info.Size() != atomicWriteRenameProbeNonceSize {
		return false, nil
	}
	nonce, err := hex.DecodeString(expected.Nonce)
	if err != nil {
		return false, err
	}
	content := make([]byte, len(nonce)+1)
	read, readErr := file.ReadAt(content, 0)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, readErr
	}
	if read != len(nonce) || !bytes.Equal(content[:read], nonce) {
		return false, nil
	}
	sum := sha256.Sum256(content[:read])
	return hex.EncodeToString(sum[:]) == expected.SHA256, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) moveObservedObjectToIsolation(
	manifest atomicWriteRenameProbeJournalManifest,
	fromLocation string,
	targetRel string,
	targetLocation string,
) error {
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	sourceRoot, sourceRel, sourceDir, err := ctx.location(manifest, fromLocation)
	if err != nil {
		return err
	}
	targetRoot, _, targetDir, err := ctx.location(manifest, targetLocation)
	if err != nil {
		return err
	}
	if err := rootio.RenameLeafBetweenRootsNoReplace(
		sourceRoot,
		sourceRel,
		targetRoot,
		targetRel,
	); err != nil {
		return err
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	if err := errors.Join(
		syncAtomicWriteRenameProbeDir(sourceDir, "probe recovery source"),
		syncAtomicWriteRenameProbeDir(targetDir, "probe recovery isolation"),
	); err != nil {
		return err
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return err
	}
	return callAtomicWriteRenameProbeFaultHook("namespace:recovery_" + targetLocation)
}

func (ctx *atomicWriteRenameProbeJournalContext) removeIsolatedObject(
	manifest atomicWriteRenameProbeJournalManifest,
	source bool,
) error {
	location := atomicWriteRenameProbeLocationPeerIsolation
	rel := manifest.Paths.PeerIsolation
	expected := manifest.Peer
	label := "peer"
	if source {
		location = atomicWriteRenameProbeLocationSourceIsolation
		rel = manifest.Paths.SourceIsolation
		expected = manifest.Source
		label = "source"
	}
	observed, err := ctx.inspectLayout(manifest)
	if err != nil {
		return err
	}
	current := observed.peer
	if source {
		current = observed.source
	}
	if current == atomicWriteRenameProbeLocationMissing {
		return nil
	}
	if current != location {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	if err := ctx.removeJournalObjectChecked(rel, expected, "recovery_"+label); err != nil {
		return err
	}
	return callAtomicWriteRenameProbeFaultHook("namespace:recovery_" + label + "_removed")
}

func (ctx *atomicWriteRenameProbeJournalContext) removeJournalObjectChecked(
	rel string,
	expected atomicWriteRenameProbeJournalObject,
	label string,
) error {
	file, err := rootio.OpenRegularFileNoFollow(ctx.internalRoot, rel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	matches, err := atomicWriteRenameProbeJournalObjectMatches(file, info, expected)
	if err != nil {
		return err
	}
	if !matches {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return ctx.removeJournalLeafChecked(filepath.Base(rel), file, info, label)
}

func (ctx *atomicWriteRenameProbeJournalContext) removeJournalLeafChecked(
	leaf string,
	opened *os.File,
	info os.FileInfo,
	label string,
) error {
	if err := ctx.verifyJournalDirectoryEntry(); err != nil {
		return err
	}
	if opened == nil || info == nil || !info.Mode().IsRegular() {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	persistentIdentity := workspace.PersistentIdentityTokenForFileInfo(info)
	deleteIdentity := workspace.DeleteIdentityTokenForFileInfo(info)
	if persistentIdentity == "" || deleteIdentity == "" {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	if err := atomicWriteRenameProbeBeforeCheckedRemove(label); err != nil {
		return err
	}
	verify := func(_ string, current os.FileInfo) error {
		if current == nil || !current.Mode().IsRegular() ||
			workspace.PersistentIdentityTokenForFileInfo(current) != persistentIdentity ||
			workspace.DeleteIdentityTokenForFileInfo(current) != deleteIdentity ||
			current.Mode() != info.Mode() ||
			current.Size() != info.Size() ||
			!current.ModTime().Equal(info.ModTime()) {
			return errAtomicWriteRenameProbeOwnershipChanged
		}
		return nil
	}
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(ctx.journalDir, leaf, verify); err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if err := ctx.journalDir.Sync(); err != nil {
		return err
	}
	after, err := opened.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if !os.SameFile(info, after) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) writeRecoveryPhase(
	manifest atomicWriteRenameProbeJournalManifest,
	index int,
	phase string,
) error {
	record := atomicWriteRenameProbePhaseRecord{
		Schema:      atomicWriteRenameProbeJournalSchema,
		OperationID: manifest.OperationID,
		Index:       index,
		Phase:       phase,
	}
	if err := ctx.writeJournalJSON(atomicWriteRenameProbeRecoveryPhaseName(index), record); err != nil {
		return fmt.Errorf("persist atomic write rename probe recovery phase %s: %w", phase, err)
	}
	return callAtomicWriteRenameProbeFaultHook("checkpoint:recovery_" + phase)
}

func (ctx *atomicWriteRenameProbeJournalContext) finishJournalCleanup(
	entries map[string]os.DirEntry,
) error {
	var cleanup atomicWriteRenameProbeCleanupRecord
	if err := ctx.readJournalJSON(atomicWriteRenameProbeCleanupName, &cleanup); err != nil {
		return fmt.Errorf("read atomic write rename probe cleanup marker: %w", err)
	}
	if cleanup.Schema != atomicWriteRenameProbeJournalSchema {
		return errors.New("atomic write rename probe cleanup schema is invalid")
	}
	manifest := cleanup.Manifest
	if err := validateAtomicWriteRenameProbeManifest(manifest); err != nil {
		return err
	}
	if err := ctx.validateRootBindings(manifest); err != nil {
		return err
	}
	observed, err := ctx.inspectLayout(manifest)
	if err != nil {
		return err
	}
	if observed.source != atomicWriteRenameProbeLocationMissing ||
		observed.peer != atomicWriteRenameProbeLocationMissing {
		return errors.New("atomic write rename probe cleanup marker has live probe objects")
	}
	if err := validateAtomicWriteRenameProbeJournalEntryNames(entries); err != nil {
		return err
	}
	if err := ctx.validateCleanupMetadata(entries, manifest); err != nil {
		return err
	}
	for name := range entries {
		if _, ok := ctx.validatedJournalFile[name]; !ok {
			return fmt.Errorf(
				"atomic write rename probe cleanup entry %q lacks validated evidence: %w",
				name,
				errAtomicWriteRenameProbeOwnershipChanged,
			)
		}
	}

	names := make([]string, 0, len(entries))
	for name := range entries {
		if name != atomicWriteRenameProbeCleanupName {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.removeJournalMetadataFile(name); err != nil {
			return err
		}
	}
	if err := ctx.removeJournalMetadataFile(atomicWriteRenameProbeCleanupName); err != nil {
		return err
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) validateCleanupMetadata(
	entries map[string]os.DirEntry,
	manifest atomicWriteRenameProbeJournalManifest,
) error {
	setup := atomicWriteRenameProbeSetupRecord{
		Schema:       atomicWriteRenameProbeJournalSchema,
		OperationID:  manifest.OperationID,
		FilesRoot:    manifest.FilesRoot,
		InternalRoot: manifest.InternalRoot,
		StagingRoot:  manifest.StagingRoot,
		JournalRoot:  manifest.JournalRoot,
		Paths:        manifest.Paths,
		Source: atomicWriteRenameProbeSetupObject{
			Nonce:  manifest.Source.Nonce,
			SHA256: manifest.Source.SHA256,
		},
		Peer: atomicWriteRenameProbeSetupObject{
			Nonce:  manifest.Peer.Nonce,
			SHA256: manifest.Peer.SHA256,
		},
	}
	if _, exists := entries[atomicWriteRenameProbeSetupName]; exists {
		var current atomicWriteRenameProbeSetupRecord
		if err := ctx.readJournalJSON(atomicWriteRenameProbeSetupName, &current); err != nil {
			return fmt.Errorf("validate cleanup setup intent: %w", err)
		}
		if current != setup {
			return errors.New("atomic write rename probe cleanup setup intent changed")
		}
	}
	for _, bindingSpec := range []struct {
		name     string
		role     string
		expected atomicWriteRenameProbeJournalObject
	}{
		{atomicWriteRenameProbeSourceBindingName, "source", manifest.Source},
		{atomicWriteRenameProbePeerBindingName, "peer", manifest.Peer},
	} {
		if _, exists := entries[bindingSpec.name]; !exists {
			continue
		}
		var binding atomicWriteRenameProbeBindingRecord
		if err := ctx.readJournalJSON(bindingSpec.name, &binding); err != nil {
			return fmt.Errorf("validate cleanup %s binding: %w", bindingSpec.role, err)
		}
		if err := validateAtomicWriteRenameProbeBindingRecord(setup, binding, bindingSpec.role); err != nil {
			return err
		}
		if binding.Object != bindingSpec.expected {
			return errors.New("atomic write rename probe cleanup binding changed")
		}
	}
	if _, exists := entries[atomicWriteRenameProbeManifestName]; exists {
		var current atomicWriteRenameProbeJournalManifest
		if err := ctx.readJournalJSON(atomicWriteRenameProbeManifestName, &current); err != nil {
			return fmt.Errorf("validate cleanup manifest: %w", err)
		}
		if current != manifest {
			return errors.New("atomic write rename probe cleanup manifest changed")
		}
	}
	for index, definition := range atomicWriteRenameProbePhases {
		name := atomicWriteRenameProbePhaseName(index)
		if _, exists := entries[name]; !exists {
			continue
		}
		var record atomicWriteRenameProbePhaseRecord
		if err := ctx.readJournalJSON(name, &record); err != nil {
			return fmt.Errorf("validate cleanup phase %d: %w", index, err)
		}
		if record.Schema != atomicWriteRenameProbeJournalSchema ||
			record.OperationID != manifest.OperationID ||
			record.Index != index ||
			record.Phase != definition.name {
			return errors.New("atomic write rename probe cleanup phase record changed")
		}
	}
	if _, exists := entries[atomicWriteRenameProbeRecoveryName]; exists {
		var recovery atomicWriteRenameProbeRecoveryRecord
		if err := ctx.readJournalJSON(atomicWriteRenameProbeRecoveryName, &recovery); err != nil {
			return fmt.Errorf("validate cleanup recovery record: %w", err)
		}
		if err := validateAtomicWriteRenameProbeRecoveryRecord(manifest, recovery); err != nil {
			return err
		}
	}
	for index, phase := range []string{"", "source_isolated", "source_removed", "peer_isolated", "peer_removed"} {
		if index == 0 {
			continue
		}
		name := atomicWriteRenameProbeRecoveryPhaseName(index)
		if _, exists := entries[name]; !exists {
			continue
		}
		var record atomicWriteRenameProbePhaseRecord
		if err := ctx.readJournalJSON(name, &record); err != nil {
			return fmt.Errorf("validate cleanup recovery phase %d: %w", index, err)
		}
		if record.Schema != atomicWriteRenameProbeJournalSchema ||
			record.OperationID != manifest.OperationID ||
			record.Index != index ||
			record.Phase != phase {
			return errors.New("atomic write rename probe cleanup recovery phase changed")
		}
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) removeJournalMetadataFile(name string) error {
	expected, ok := ctx.validatedJournalFile[name]
	if !ok {
		return fmt.Errorf(
			"atomic write rename probe metadata %q lacks validated evidence: %w",
			name,
			errAtomicWriteRenameProbeOwnershipChanged,
		)
	}
	if err := atomicWriteRenameProbeBeforeMetadataRemove(name); err != nil {
		return err
	}
	file, info, _, actual, err := ctx.openStableJournalFile(name)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	defer file.Close()
	if actual != expected {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	if err := ctx.removeJournalLeafChecked(name, file, info, "metadata:"+name); err != nil {
		return err
	}
	delete(ctx.validatedJournalFile, name)
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) readActiveJournal(
	entries map[string]os.DirEntry,
) (
	atomicWriteRenameProbeJournalManifest,
	int,
	*atomicWriteRenameProbeRecoveryRecord,
	int,
	error,
) {
	if err := validateAtomicWriteRenameProbeJournalEntryNames(entries); err != nil {
		return atomicWriteRenameProbeJournalManifest{}, 0, nil, 0, err
	}
	for _, required := range []string{
		atomicWriteRenameProbeSetupName,
		atomicWriteRenameProbeSourceBindingName,
		atomicWriteRenameProbePeerBindingName,
	} {
		if _, ok := entries[required]; !ok {
			return atomicWriteRenameProbeJournalManifest{}, 0, nil, 0, fmt.Errorf(
				"atomic write rename probe active journal is missing %s",
				required,
			)
		}
	}
	if _, ok := entries[atomicWriteRenameProbeManifestName]; !ok {
		return atomicWriteRenameProbeJournalManifest{}, 0, nil, 0, errors.New("atomic write rename probe manifest is missing")
	}
	var manifest atomicWriteRenameProbeJournalManifest
	if err := ctx.readJournalJSON(atomicWriteRenameProbeManifestName, &manifest); err != nil {
		return manifest, 0, nil, 0, fmt.Errorf("read atomic write rename probe manifest: %w", err)
	}
	if err := validateAtomicWriteRenameProbeManifest(manifest); err != nil {
		return manifest, 0, nil, 0, err
	}
	if err := ctx.validateActiveSetupMetadata(entries, manifest); err != nil {
		return manifest, 0, nil, 0, err
	}

	lastPhase := -1
	for index, definition := range atomicWriteRenameProbePhases {
		name := atomicWriteRenameProbePhaseName(index)
		_, exists := entries[name]
		if !exists {
			for later := index + 1; later < len(atomicWriteRenameProbePhases); later++ {
				if _, laterExists := entries[atomicWriteRenameProbePhaseName(later)]; laterExists {
					return manifest, 0, nil, 0, errors.New("atomic write rename probe phase sequence has a gap")
				}
			}
			break
		}
		var record atomicWriteRenameProbePhaseRecord
		if err := ctx.readJournalJSON(name, &record); err != nil {
			return manifest, 0, nil, 0, fmt.Errorf("read atomic write rename probe phase %d: %w", index, err)
		}
		if record.Schema != atomicWriteRenameProbeJournalSchema ||
			record.OperationID != manifest.OperationID ||
			record.Index != index ||
			record.Phase != definition.name {
			return manifest, 0, nil, 0, errors.New("atomic write rename probe phase record is invalid")
		}
		lastPhase = index
	}

	var recovery *atomicWriteRenameProbeRecoveryRecord
	lastRecoveryPhase := 0
	if _, exists := entries[atomicWriteRenameProbeRecoveryName]; exists {
		var record atomicWriteRenameProbeRecoveryRecord
		if err := ctx.readJournalJSON(atomicWriteRenameProbeRecoveryName, &record); err != nil {
			return manifest, 0, nil, 0, fmt.Errorf("read atomic write rename probe recovery record: %w", err)
		}
		if err := validateAtomicWriteRenameProbeRecoveryRecord(manifest, record); err != nil {
			return manifest, 0, nil, 0, err
		}
		recovery = &record
		for index, phase := range []string{"", "source_isolated", "source_removed", "peer_isolated", "peer_removed"} {
			if index == 0 {
				continue
			}
			name := atomicWriteRenameProbeRecoveryPhaseName(index)
			_, exists := entries[name]
			if !exists {
				for later := index + 1; later <= 4; later++ {
					if _, laterExists := entries[atomicWriteRenameProbeRecoveryPhaseName(later)]; laterExists {
						return manifest, 0, nil, 0, errors.New("atomic write rename probe recovery phase sequence has a gap")
					}
				}
				break
			}
			var phaseRecord atomicWriteRenameProbePhaseRecord
			if err := ctx.readJournalJSON(name, &phaseRecord); err != nil {
				return manifest, 0, nil, 0, fmt.Errorf("read atomic write rename probe recovery phase %d: %w", index, err)
			}
			if phaseRecord.Schema != atomicWriteRenameProbeJournalSchema ||
				phaseRecord.OperationID != manifest.OperationID ||
				phaseRecord.Index != index ||
				phaseRecord.Phase != phase {
				return manifest, 0, nil, 0, errors.New("atomic write rename probe recovery phase record is invalid")
			}
			lastRecoveryPhase = index
		}
	} else {
		for index := 1; index <= 4; index++ {
			if _, exists := entries[atomicWriteRenameProbeRecoveryPhaseName(index)]; exists {
				return manifest, 0, nil, 0, errors.New("atomic write rename probe recovery phase lacks its recovery record")
			}
		}
	}
	return manifest, lastPhase, recovery, lastRecoveryPhase, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) validateActiveSetupMetadata(
	entries map[string]os.DirEntry,
	manifest atomicWriteRenameProbeJournalManifest,
) error {
	var setup atomicWriteRenameProbeSetupRecord
	if err := ctx.readJournalJSON(atomicWriteRenameProbeSetupName, &setup); err != nil {
		return fmt.Errorf("read atomic write rename probe setup intent: %w", err)
	}
	if err := validateAtomicWriteRenameProbeSetupRecord(setup); err != nil {
		return err
	}
	if setup.OperationID != manifest.OperationID ||
		setup.FilesRoot != manifest.FilesRoot ||
		setup.InternalRoot != manifest.InternalRoot ||
		setup.StagingRoot != manifest.StagingRoot ||
		setup.JournalRoot != manifest.JournalRoot ||
		setup.Paths != manifest.Paths ||
		setup.Source.Nonce != manifest.Source.Nonce ||
		setup.Source.SHA256 != manifest.Source.SHA256 ||
		setup.Peer.Nonce != manifest.Peer.Nonce ||
		setup.Peer.SHA256 != manifest.Peer.SHA256 {
		return errors.New("atomic write rename probe manifest does not match setup intent")
	}
	for _, bindingSpec := range []struct {
		name     string
		role     string
		expected atomicWriteRenameProbeJournalObject
	}{
		{atomicWriteRenameProbeSourceBindingName, "source", manifest.Source},
		{atomicWriteRenameProbePeerBindingName, "peer", manifest.Peer},
	} {
		if _, exists := entries[bindingSpec.name]; !exists {
			return fmt.Errorf("atomic write rename probe %s binding is missing", bindingSpec.role)
		}
		var binding atomicWriteRenameProbeBindingRecord
		if err := ctx.readJournalJSON(bindingSpec.name, &binding); err != nil {
			return fmt.Errorf("read atomic write rename probe %s binding: %w", bindingSpec.role, err)
		}
		if err := validateAtomicWriteRenameProbeBindingRecord(setup, binding, bindingSpec.role); err != nil {
			return err
		}
		if binding.Object != bindingSpec.expected {
			return errors.New("atomic write rename probe binding does not match manifest")
		}
	}
	return nil
}

func validateAtomicWriteRenameProbeJournalEntryNames(entries map[string]os.DirEntry) error {
	allowed := map[string]struct{}{
		atomicWriteRenameProbeSetupName:                                          {},
		atomicWriteRenameProbeSourceBindingName:                                  {},
		atomicWriteRenameProbePeerBindingName:                                    {},
		atomicWriteRenameProbeManifestName:                                       {},
		atomicWriteRenameProbeRecoveryName:                                       {},
		atomicWriteRenameProbeCleanupName:                                        {},
		atomicWriteRenameProbeSourceIsolation:                                    {},
		atomicWriteRenameProbePeerIsolation:                                      {},
		atomicWriteRenameProbePendingName(atomicWriteRenameProbeSourceIsolation): {},
		atomicWriteRenameProbePendingName(atomicWriteRenameProbePeerIsolation):   {},
	}
	for index := range atomicWriteRenameProbePhases {
		allowed[atomicWriteRenameProbePhaseName(index)] = struct{}{}
	}
	for index := 1; index <= 4; index++ {
		allowed[atomicWriteRenameProbeRecoveryPhaseName(index)] = struct{}{}
	}
	metadataNames := make([]string, 0, len(allowed))
	for name := range allowed {
		if name == atomicWriteRenameProbeSourceIsolation ||
			name == atomicWriteRenameProbePeerIsolation ||
			strings.HasPrefix(name, atomicWriteRenameProbePendingPrefix) {
			continue
		}
		metadataNames = append(metadataNames, name)
	}
	for _, name := range metadataNames {
		allowed[atomicWriteRenameProbePendingName(name)] = struct{}{}
	}
	for name := range entries {
		if _, ok := allowed[name]; !ok {
			return fmt.Errorf("atomic write rename probe journal contains unknown entry %q", name)
		}
	}
	return nil
}

func validateAtomicWriteRenameProbeManifest(manifest atomicWriteRenameProbeJournalManifest) error {
	if manifest.Schema != atomicWriteRenameProbeJournalSchema {
		return errors.New("atomic write rename probe manifest schema is invalid")
	}
	operationIDBytes, err := hex.DecodeString(manifest.OperationID)
	if err != nil || len(operationIDBytes) != 16 || manifest.OperationID != strings.ToLower(manifest.OperationID) {
		return errors.New("atomic write rename probe operation ID is invalid")
	}
	expectedPaths := atomicWriteRenameProbeJournalPaths{
		Source:          filepath.Join(writeStagingDir, atomicWriteRenameProbePrefix+manifest.OperationID+"-source"),
		Peer:            filepath.Join(writeStagingDir, atomicWriteRenameProbePrefix+manifest.OperationID+"-peer"),
		FilesSlot:       atomicWriteRenameProbePrefix + manifest.OperationID + "-slot",
		SourceIsolation: filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSourceIsolation),
		PeerIsolation:   filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbePeerIsolation),
	}
	if manifest.Paths != expectedPaths {
		return errors.New("atomic write rename probe manifest paths are invalid")
	}
	for _, root := range []atomicWriteRenameProbeJournalRoot{
		manifest.FilesRoot,
		manifest.InternalRoot,
		manifest.StagingRoot,
		manifest.JournalRoot,
	} {
		if len(root.PersistentIdentity) != sha256.Size*2 || root.Mode&uint32(os.ModeDir) == 0 {
			return errors.New("atomic write rename probe root identity is invalid")
		}
	}
	if manifest.StagingRoot.Mode != uint32(os.ModeDir|0o700) ||
		manifest.JournalRoot.Mode != uint32(os.ModeDir|0o700) {
		return errors.New("atomic write rename probe private root mode is invalid")
	}
	if err := validateAtomicWriteRenameProbeObjectManifest(manifest.Source); err != nil {
		return fmt.Errorf("invalid source object manifest: %w", err)
	}
	if err := validateAtomicWriteRenameProbeObjectManifest(manifest.Peer); err != nil {
		return fmt.Errorf("invalid peer object manifest: %w", err)
	}
	if manifest.Source.Nonce == manifest.Peer.Nonce ||
		manifest.Source.PersistentIdentity == manifest.Peer.PersistentIdentity {
		return errors.New("atomic write rename probe objects are not independent")
	}
	return nil
}

func validateAtomicWriteRenameProbeSetupRecord(setup atomicWriteRenameProbeSetupRecord) error {
	if setup.Schema != atomicWriteRenameProbeJournalSchema {
		return errors.New("atomic write rename probe setup schema is invalid")
	}
	operationIDBytes, err := hex.DecodeString(setup.OperationID)
	if err != nil || len(operationIDBytes) != 16 || setup.OperationID != strings.ToLower(setup.OperationID) {
		return errors.New("atomic write rename probe setup operation ID is invalid")
	}
	expectedPaths := atomicWriteRenameProbeJournalPaths{
		Source:          filepath.Join(writeStagingDir, atomicWriteRenameProbePrefix+setup.OperationID+"-source"),
		Peer:            filepath.Join(writeStagingDir, atomicWriteRenameProbePrefix+setup.OperationID+"-peer"),
		FilesSlot:       atomicWriteRenameProbePrefix + setup.OperationID + "-slot",
		SourceIsolation: filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbeSourceIsolation),
		PeerIsolation:   filepath.Join(atomicWriteRenameProbeJournalDir, atomicWriteRenameProbePeerIsolation),
	}
	if setup.Paths != expectedPaths {
		return errors.New("atomic write rename probe setup paths are invalid")
	}
	for _, root := range []atomicWriteRenameProbeJournalRoot{
		setup.FilesRoot,
		setup.InternalRoot,
		setup.StagingRoot,
		setup.JournalRoot,
	} {
		if len(root.PersistentIdentity) != sha256.Size*2 || root.Mode&uint32(os.ModeDir) == 0 {
			return errors.New("atomic write rename probe setup root identity is invalid")
		}
	}
	if setup.StagingRoot.Mode != uint32(os.ModeDir|0o700) ||
		setup.JournalRoot.Mode != uint32(os.ModeDir|0o700) {
		return errors.New("atomic write rename probe setup private root mode is invalid")
	}
	if err := validateAtomicWriteRenameProbeSetupObject(setup.Source); err != nil {
		return fmt.Errorf("invalid setup source object: %w", err)
	}
	if err := validateAtomicWriteRenameProbeSetupObject(setup.Peer); err != nil {
		return fmt.Errorf("invalid setup peer object: %w", err)
	}
	if setup.Source.Nonce == setup.Peer.Nonce {
		return errors.New("atomic write rename probe setup objects are not independent")
	}
	return nil
}

func validateAtomicWriteRenameProbeSetupObject(object atomicWriteRenameProbeSetupObject) error {
	nonce, err := hex.DecodeString(object.Nonce)
	if err != nil || len(nonce) != atomicWriteRenameProbeNonceSize ||
		object.Nonce != strings.ToLower(object.Nonce) {
		return errors.New("nonce is invalid")
	}
	hash, err := hex.DecodeString(object.SHA256)
	expectedHash := sha256.Sum256(nonce)
	if err != nil || len(hash) != sha256.Size ||
		object.SHA256 != strings.ToLower(object.SHA256) ||
		!bytes.Equal(hash, expectedHash[:]) {
		return errors.New("SHA-256 digest is invalid")
	}
	return nil
}

func validateAtomicWriteRenameProbeBindingRecord(
	setup atomicWriteRenameProbeSetupRecord,
	binding atomicWriteRenameProbeBindingRecord,
	role string,
) error {
	if binding.Schema != atomicWriteRenameProbeJournalSchema ||
		binding.OperationID != setup.OperationID ||
		binding.Role != role {
		return errors.New("atomic write rename probe binding record is invalid")
	}
	if err := validateAtomicWriteRenameProbeObjectManifest(binding.Object); err != nil {
		return fmt.Errorf("invalid atomic write rename probe %s binding: %w", role, err)
	}
	expected := setup.Peer
	if role == "source" {
		expected = setup.Source
	}
	if binding.Object.Nonce != expected.Nonce || binding.Object.SHA256 != expected.SHA256 {
		return errors.New("atomic write rename probe binding does not match setup intent")
	}
	return nil
}

func validateAtomicWriteRenameProbeObjectManifest(object atomicWriteRenameProbeJournalObject) error {
	nonce, err := hex.DecodeString(object.Nonce)
	if err != nil || len(nonce) != atomicWriteRenameProbeNonceSize ||
		object.Nonce != strings.ToLower(object.Nonce) {
		return errors.New("nonce is invalid")
	}
	hash, err := hex.DecodeString(object.SHA256)
	expectedHash := sha256.Sum256(nonce)
	if err != nil || len(hash) != sha256.Size ||
		object.SHA256 != strings.ToLower(object.SHA256) ||
		!bytes.Equal(hash, expectedHash[:]) {
		return errors.New("SHA-256 digest is invalid")
	}
	if len(object.PersistentIdentity) != sha256.Size*2 ||
		object.Mode != uint32(0o600) ||
		object.Size != atomicWriteRenameProbeNonceSize ||
		object.ModTimeUnixNano <= 0 {
		return errors.New("identity or metadata is invalid")
	}
	return nil
}

func validateAtomicWriteRenameProbeRecoveryRecord(
	manifest atomicWriteRenameProbeJournalManifest,
	recovery atomicWriteRenameProbeRecoveryRecord,
) error {
	if recovery.Schema != atomicWriteRenameProbeJournalSchema ||
		recovery.OperationID != manifest.OperationID ||
		!atomicWriteRenameProbeLocationValidForSource(recovery.SourceFrom) ||
		!atomicWriteRenameProbeLocationValidForPeer(recovery.PeerFrom) {
		return errors.New("atomic write rename probe recovery record is invalid")
	}
	return nil
}

func atomicWriteRenameProbeLocationValidForSource(location string) bool {
	switch location {
	case atomicWriteRenameProbeLocationMissing,
		atomicWriteRenameProbeLocationSource,
		atomicWriteRenameProbeLocationFiles,
		atomicWriteRenameProbeLocationSourceIsolation:
		return true
	default:
		return false
	}
}

func atomicWriteRenameProbeLocationValidForPeer(location string) bool {
	switch location {
	case atomicWriteRenameProbeLocationMissing,
		atomicWriteRenameProbeLocationPeer,
		atomicWriteRenameProbeLocationSource,
		atomicWriteRenameProbeLocationFiles,
		atomicWriteRenameProbeLocationPeerIsolation:
		return true
	default:
		return false
	}
}

func (ctx *atomicWriteRenameProbeJournalContext) validateRootBindings(
	manifest atomicWriteRenameProbeJournalManifest,
) error {
	return ctx.validateRootBindingValues(
		manifest.FilesRoot,
		manifest.InternalRoot,
		manifest.StagingRoot,
		manifest.JournalRoot,
	)
}

func (ctx *atomicWriteRenameProbeJournalContext) validateSetupRootBindings(
	setup atomicWriteRenameProbeSetupRecord,
) error {
	return ctx.validateRootBindingValues(
		setup.FilesRoot,
		setup.InternalRoot,
		setup.StagingRoot,
		setup.JournalRoot,
	)
}

func (ctx *atomicWriteRenameProbeJournalContext) validateRootBindingValues(
	filesRoot atomicWriteRenameProbeJournalRoot,
	internalRoot atomicWriteRenameProbeJournalRoot,
	stagingRoot atomicWriteRenameProbeJournalRoot,
	journalRoot atomicWriteRenameProbeJournalRoot,
) error {
	if err := errors.Join(
		ctx.verifyStagingDirectoryEntry(),
		ctx.verifyJournalDirectoryEntry(),
	); err != nil {
		return err
	}
	for _, binding := range []struct {
		label    string
		file     *os.File
		expected atomicWriteRenameProbeJournalRoot
	}{
		{"files root", ctx.filesDir, filesRoot},
		{"internal root", ctx.internalDir, internalRoot},
		{"write staging", ctx.stagingDir, stagingRoot},
		{"probe journal", ctx.journalDir, journalRoot},
	} {
		actual, err := atomicWriteRenameProbeRootIdentity(binding.file)
		if err != nil {
			return fmt.Errorf("verify %s binding: %w", binding.label, err)
		}
		if actual != binding.expected {
			return fmt.Errorf("%s binding changed: %w", binding.label, errAtomicWriteRenameProbeOwnershipChanged)
		}
	}
	return nil
}

func validateAtomicWriteRenameProbePhaseLayout(
	lastPhase int,
	observed atomicWriteRenameProbeObservedLayout,
) error {
	expected := atomicWriteRenameProbeLayout{
		source: atomicWriteRenameProbeLocationSourceIsolation,
		peer:   atomicWriteRenameProbeLocationPeerIsolation,
	}
	var next atomicWriteRenameProbeLayout
	if lastPhase >= 0 {
		if lastPhase >= len(atomicWriteRenameProbePhases) {
			return errors.New("atomic write rename probe phase index is invalid")
		}
		expected = atomicWriteRenameProbePhases[lastPhase].current
		next = atomicWriteRenameProbePhases[lastPhase].next
	}
	if atomicWriteRenameProbeLayoutMatches(expected, observed) {
		return nil
	}
	if next != (atomicWriteRenameProbeLayout{}) &&
		atomicWriteRenameProbeLayoutMatches(next, observed) {
		return nil
	}
	return fmt.Errorf(
		"atomic write rename probe layout does not match phase %d: %w",
		lastPhase,
		errAtomicWriteRenameProbeOwnershipChanged,
	)
}

func atomicWriteRenameProbeLayoutMatches(
	expected atomicWriteRenameProbeLayout,
	observed atomicWriteRenameProbeObservedLayout,
) bool {
	return expected.source == observed.source && expected.peer == observed.peer
}

func validateAtomicWriteRenameProbeRecoveryLayout(
	recovery atomicWriteRenameProbeRecoveryRecord,
	lastPhase int,
	observed atomicWriteRenameProbeObservedLayout,
) error {
	sourceAllowed := map[string]bool{}
	peerAllowed := map[string]bool{}
	switch lastPhase {
	case 0:
		sourceAllowed[recovery.SourceFrom] = true
		sourceAllowed[atomicWriteRenameProbeLocationSourceIsolation] = true
		if recovery.SourceFrom == atomicWriteRenameProbeLocationMissing {
			sourceAllowed[atomicWriteRenameProbeLocationMissing] = true
		}
		peerAllowed[recovery.PeerFrom] = true
	case 1:
		sourceAllowed[atomicWriteRenameProbeLocationSourceIsolation] = true
		sourceAllowed[atomicWriteRenameProbeLocationMissing] = true
		peerAllowed[recovery.PeerFrom] = true
	case 2:
		sourceAllowed[atomicWriteRenameProbeLocationMissing] = true
		peerAllowed[recovery.PeerFrom] = true
		peerAllowed[atomicWriteRenameProbeLocationPeerIsolation] = true
	case 3:
		sourceAllowed[atomicWriteRenameProbeLocationMissing] = true
		peerAllowed[atomicWriteRenameProbeLocationPeerIsolation] = true
		peerAllowed[atomicWriteRenameProbeLocationMissing] = true
	case 4:
		sourceAllowed[atomicWriteRenameProbeLocationMissing] = true
		peerAllowed[atomicWriteRenameProbeLocationMissing] = true
	default:
		return errors.New("atomic write rename probe recovery phase is invalid")
	}
	if !sourceAllowed[observed.source] || !peerAllowed[observed.peer] {
		return fmt.Errorf("atomic write rename probe recovery layout changed: %w", errAtomicWriteRenameProbeOwnershipChanged)
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) verifyExpectedLayout(
	manifest *atomicWriteRenameProbeJournalManifest,
	expected atomicWriteRenameProbeLayout,
) error {
	if err := ctx.validateRootBindings(*manifest); err != nil {
		return err
	}
	observed, err := ctx.inspectLayout(*manifest)
	if err != nil {
		return err
	}
	if !atomicWriteRenameProbeLayoutMatches(expected, observed) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) inspectLayout(
	manifest atomicWriteRenameProbeJournalManifest,
) (atomicWriteRenameProbeObservedLayout, error) {
	observed := atomicWriteRenameProbeObservedLayout{
		source: atomicWriteRenameProbeLocationMissing,
		peer:   atomicWriteRenameProbeLocationMissing,
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return observed, err
	}
	for _, location := range []string{
		atomicWriteRenameProbeLocationSource,
		atomicWriteRenameProbeLocationPeer,
		atomicWriteRenameProbeLocationFiles,
		atomicWriteRenameProbeLocationSourceIsolation,
		atomicWriteRenameProbeLocationPeerIsolation,
	} {
		root, rel, _, err := ctx.location(manifest, location)
		if err != nil {
			return observed, err
		}
		owner, err := ctx.identifyObjectAt(root, rel, manifest)
		if err != nil {
			return observed, fmt.Errorf("inspect journaled probe location %s: %w", location, err)
		}
		switch owner {
		case "":
			continue
		case "source":
			if observed.source != atomicWriteRenameProbeLocationMissing {
				return observed, errAtomicWriteRenameProbeOwnershipChanged
			}
			observed.source = location
		case "peer":
			if observed.peer != atomicWriteRenameProbeLocationMissing {
				return observed, errAtomicWriteRenameProbeOwnershipChanged
			}
			observed.peer = location
		default:
			return observed, errAtomicWriteRenameProbeOwnershipChanged
		}
	}
	if err := ctx.verifyPrivateDirectoryEntries(); err != nil {
		return observed, err
	}
	return observed, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) location(
	manifest atomicWriteRenameProbeJournalManifest,
	location string,
) (*os.Root, string, *os.File, error) {
	switch location {
	case atomicWriteRenameProbeLocationSource:
		return ctx.internalRoot, manifest.Paths.Source, ctx.stagingDir, nil
	case atomicWriteRenameProbeLocationPeer:
		return ctx.internalRoot, manifest.Paths.Peer, ctx.stagingDir, nil
	case atomicWriteRenameProbeLocationFiles:
		return ctx.filesRoot, manifest.Paths.FilesSlot, ctx.filesDir, nil
	case atomicWriteRenameProbeLocationSourceIsolation:
		return ctx.internalRoot, manifest.Paths.SourceIsolation, ctx.journalDir, nil
	case atomicWriteRenameProbeLocationPeerIsolation:
		return ctx.internalRoot, manifest.Paths.PeerIsolation, ctx.journalDir, nil
	default:
		return nil, "", nil, errors.New("atomic write rename probe location is invalid")
	}
}

func (ctx *atomicWriteRenameProbeJournalContext) identifyObjectAt(
	root *os.Root,
	rel string,
	manifest atomicWriteRenameProbeJournalManifest,
) (string, error) {
	info, err := root.Lstat(rel)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errAtomicWriteRenameProbeOwnershipChanged
	}
	file, err := rootio.OpenRegularFileNoFollow(root, rel)
	if err != nil {
		return "", errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return "", errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	sourceMatches, err := atomicWriteRenameProbeJournalObjectMatches(file, openedInfo, manifest.Source)
	if err != nil {
		return "", err
	}
	peerMatches, err := atomicWriteRenameProbeJournalObjectMatches(file, openedInfo, manifest.Peer)
	if err != nil {
		return "", err
	}
	if sourceMatches == peerMatches {
		return "", errAtomicWriteRenameProbeOwnershipChanged
	}
	after, err := root.Lstat(rel)
	if err != nil || !os.SameFile(openedInfo, after) {
		return "", errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if sourceMatches {
		return "source", nil
	}
	return "peer", nil
}

func (ctx *atomicWriteRenameProbeJournalContext) verifyObjectAt(
	root *os.Root,
	rel string,
	expected atomicWriteRenameProbeJournalObject,
) error {
	file, err := rootio.OpenRegularFileNoFollow(root, rel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	matches, err := atomicWriteRenameProbeJournalObjectMatches(file, info, expected)
	if err != nil {
		return err
	}
	if !matches {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return nil
}

func atomicWriteRenameProbeJournalObjectMatches(
	file *os.File,
	info os.FileInfo,
	expected atomicWriteRenameProbeJournalObject,
) (bool, error) {
	if file == nil || info == nil || !info.Mode().IsRegular() {
		return false, nil
	}
	if workspace.PersistentIdentityTokenForFileInfo(info) != expected.PersistentIdentity ||
		uint32(info.Mode()) != expected.Mode ||
		info.Size() != expected.Size ||
		info.ModTime().UnixNano() != expected.ModTimeUnixNano {
		return false, nil
	}
	nonce, err := hex.DecodeString(expected.Nonce)
	if err != nil {
		return false, err
	}
	content := make([]byte, len(nonce)+1)
	read, readErr := file.ReadAt(content, 0)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, readErr
	}
	if read != len(nonce) || !bytes.Equal(content[:read], nonce) {
		return false, nil
	}
	sum := sha256.Sum256(content[:read])
	return hex.EncodeToString(sum[:]) == expected.SHA256, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) readJournalDir() (map[string]os.DirEntry, error) {
	if err := ctx.verifyJournalDirectoryEntry(); err != nil {
		return nil, err
	}
	if _, err := ctx.journalDir.Seek(0, 0); err != nil {
		return nil, err
	}
	entries, err := ctx.journalDir.ReadDir(-1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	result := make(map[string]os.DirEntry, len(entries))
	for _, entry := range entries {
		if _, exists := result[entry.Name()]; exists {
			return nil, errors.New("atomic write rename probe journal contains a duplicate entry")
		}
		result[entry.Name()] = entry
	}
	return result, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) writeJournalJSON(name string, value any) error {
	if err := ctx.verifyJournalDirectoryEntry(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > atomicWriteRenameProbeMaxJournalFileSize {
		return errors.New("atomic write rename probe journal record is too large")
	}
	pendingName := atomicWriteRenameProbePendingName(name)
	pendingRel := filepath.Join(atomicWriteRenameProbeJournalDir, pendingName)
	delete(ctx.validatedJournalFile, pendingName)
	delete(ctx.validatedJournalFile, name)
	if name == atomicWriteRenameProbeSetupName {
		if err := callAtomicWriteRenameProbeFaultHook("before:journal_pending:" + name); err != nil {
			return err
		}
	}
	file, err := rootio.OpenFileNoFollow(
		ctx.internalRoot,
		pendingRel,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return err
	}
	if name == atomicWriteRenameProbeSetupName {
		if err := callAtomicWriteRenameProbeFaultHook("namespace:journal_pending_created:" + name); err != nil {
			_ = file.Close()
			return err
		}
	}
	split := len(data) / 2
	written, writeErr := file.Write(data[:split])
	if writeErr == nil && written != split {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil && name == atomicWriteRenameProbeSetupName {
		if err := callAtomicWriteRenameProbeFaultHook("namespace:journal_pending_partial:" + name); err != nil {
			_ = file.Close()
			return err
		}
	}
	if writeErr == nil {
		var tailWritten int
		tailWritten, writeErr = file.Write(data[split:])
		if writeErr == nil && tailWritten != len(data)-split {
			writeErr = io.ErrShortWrite
		}
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := callAtomicWriteRenameProbeFaultHook("checkpoint:journal_pending:" + name); err != nil {
		return err
	}
	targetRel := filepath.Join(atomicWriteRenameProbeJournalDir, name)
	if err := rootio.RenameLeafNoReplace(ctx.internalRoot, pendingRel, targetRel); err != nil {
		return err
	}
	return ctx.journalDir.Sync()
}

func (ctx *atomicWriteRenameProbeJournalContext) readJournalJSON(name string, value any) error {
	file, _, data, evidence, err := ctx.openStableJournalFile(name)
	if err != nil {
		return err
	}
	defer file.Close()
	if previous, exists := ctx.validatedJournalFile[name]; exists {
		if previous != evidence {
			return errAtomicWriteRenameProbeOwnershipChanged
		}
	} else {
		ctx.validatedJournalFile[name] = evidence
	}
	if len(data) == 0 {
		return errors.Join(
			errAtomicWriteRenameProbePendingRecordIncomplete,
			errors.New("atomic write rename probe journal record is empty"),
		)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		var syntaxErr *json.SyntaxError
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &syntaxErr) {
			return errors.Join(errAtomicWriteRenameProbePendingRecordIncomplete, err)
		}
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("atomic write rename probe journal record has trailing JSON")
		}
		return err
	}
	return atomicWriteRenameProbeAfterJournalSemanticRead(name)
}

func (ctx *atomicWriteRenameProbeJournalContext) openStableJournalFile(
	name string,
) (
	*os.File,
	os.FileInfo,
	[]byte,
	atomicWriteRenameProbeJournalFileEvidence,
	error,
) {
	if err := ctx.verifyJournalDirectoryEntry(); err != nil {
		return nil, nil, nil, atomicWriteRenameProbeJournalFileEvidence{}, err
	}
	rel := filepath.Join(atomicWriteRenameProbeJournalDir, name)
	file, err := rootio.OpenRegularFileNoFollow(ctx.internalRoot, rel)
	if err != nil {
		return nil, nil, nil, atomicWriteRenameProbeJournalFileEvidence{}, err
	}
	fail := func(err error) (
		*os.File,
		os.FileInfo,
		[]byte,
		atomicWriteRenameProbeJournalFileEvidence,
		error,
	) {
		_ = file.Close()
		return nil, nil, nil, atomicWriteRenameProbeJournalFileEvidence{}, err
	}
	rootedBefore, err := ctx.internalRoot.Lstat(rel)
	if err != nil {
		return fail(err)
	}
	before, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(rootedBefore, before) {
		return fail(errAtomicWriteRenameProbeOwnershipChanged)
	}
	beforeEvidence, err := newAtomicWriteRenameProbeEvidence(before)
	if err != nil {
		return fail(err)
	}
	if before.Size() < 0 || before.Size() > atomicWriteRenameProbeMaxJournalFileSize {
		return fail(errors.New("atomic write rename probe journal record size is invalid"))
	}
	data, err := io.ReadAll(io.LimitReader(file, atomicWriteRenameProbeMaxJournalFileSize+1))
	if err != nil {
		return fail(err)
	}
	if int64(len(data)) != before.Size() {
		return fail(errors.New("atomic write rename probe journal record changed while reading"))
	}
	after, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	rootedAfter, err := ctx.internalRoot.Lstat(rel)
	if err != nil {
		return fail(err)
	}
	afterEvidence, err := newAtomicWriteRenameProbeEvidence(after)
	if err != nil {
		return fail(err)
	}
	rootedAfterEvidence, err := newAtomicWriteRenameProbeEvidence(rootedAfter)
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(before, after) ||
		!os.SameFile(after, rootedAfter) ||
		beforeEvidence != afterEvidence ||
		afterEvidence != rootedAfterEvidence {
		return fail(errors.New("atomic write rename probe journal record changed while reading"))
	}
	return file, after, data, atomicWriteRenameProbeJournalFileEvidence{
		file: afterEvidence,
		hash: sha256.Sum256(data),
	}, nil
}

func (ctx *atomicWriteRenameProbeJournalContext) verifyJournalDirectoryEntry() error {
	if ctx == nil || ctx.internalRoot == nil || ctx.journalDir == nil {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	rooted, err := ctx.internalRoot.Lstat(atomicWriteRenameProbeJournalDir)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	held, err := ctx.journalDir.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if !rooted.IsDir() || !held.IsDir() ||
		rooted.Mode() != os.ModeDir|0o700 ||
		held.Mode() != os.ModeDir|0o700 ||
		!os.SameFile(rooted, held) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) verifyStagingDirectoryEntry() error {
	if ctx == nil || ctx.internalRoot == nil || ctx.stagingDir == nil {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	rooted, err := ctx.internalRoot.Lstat(writeStagingDir)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	held, err := ctx.stagingDir.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if !rooted.IsDir() || !held.IsDir() ||
		rooted.Mode() != os.ModeDir|0o700 ||
		held.Mode() != os.ModeDir|0o700 ||
		!os.SameFile(rooted, held) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return nil
}

func (ctx *atomicWriteRenameProbeJournalContext) verifyPrivateDirectoryEntries() error {
	return errors.Join(
		ctx.verifyStagingDirectoryEntry(),
		ctx.verifyJournalDirectoryEntry(),
	)
}

func atomicWriteRenameProbePendingName(target string) string {
	return atomicWriteRenameProbePendingPrefix + target
}

func atomicWriteRenameProbeJournalFailure(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", ErrWriteAtomicRenameUnsupported, action, err)
}
