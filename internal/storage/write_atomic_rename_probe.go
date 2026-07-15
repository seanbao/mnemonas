package storage

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/workspace"
)

// ErrWriteAtomicRenameUnsupported reports a storage layout that cannot provide
// the atomic cross-root rename semantics required by streamed writes.
var ErrWriteAtomicRenameUnsupported = errors.New("storage layout does not support atomic streamed writes")

var errAtomicWriteRenameProbeOwnershipChanged = errors.New("atomic write rename probe ownership changed")

const atomicWriteRenameProbePrefix = ".mnemonas-write-rename-probe-"
const atomicWriteRenameProbeNonceSize = 32

type atomicWriteRenameFunc func(sourceRoot *os.Root, sourceName string, targetRoot *os.Root, targetName string) error

type atomicWriteRenameProbeFile struct {
	file     *os.File
	nonce    [atomicWriteRenameProbeNonceSize]byte
	hash     [sha256.Size]byte
	evidence atomicWriteRenameProbeEvidence
	bound    bool
}

type atomicWriteRenameProbeEvidence struct {
	persistentIdentity string
	deleteIdentity     string
	mode               os.FileMode
	size               int64
	modTime            time.Time
}

type atomicWriteRenameProbeSlot struct {
	root *os.Root
	rel  string
}

func validateAtomicWriteRenamePrerequisite(filesRoot, internalRoot *os.Root) error {
	if err := probeAtomicWriteRenames(filesRoot, internalRoot); err != nil {
		return fmt.Errorf(
			"%w: files root and internal write staging require bidirectional atomic no-replace rename and atomic exchange: %w",
			ErrWriteAtomicRenameUnsupported,
			err,
		)
	}
	return nil
}

func probeAtomicWriteRenames(filesRoot, internalRoot *os.Root) error {
	return probeAtomicWriteRenamesWith(
		filesRoot,
		internalRoot,
		rootio.RenameLeafBetweenRootsNoReplace,
		rootio.ExchangeLeavesBetweenRoots,
	)
}

func probeAtomicWriteRenamesWith(
	filesRoot, internalRoot *os.Root,
	rename atomicWriteRenameFunc,
	exchange atomicWriteRenameFunc,
) error {
	if filesRoot == nil || internalRoot == nil {
		return errors.New("atomic write rename probe root is unavailable")
	}
	if rename == nil {
		return errors.New("atomic write rename operation is unavailable")
	}
	if exchange == nil {
		return errors.New("atomic write exchange operation is unavailable")
	}

	return probeAtomicWriteRenamesJournaled(filesRoot, internalRoot, rename, exchange)
}

func (fs *FileSystem) validateAtomicWriteTargetMount(name string) error {
	if fs == nil || fs.workspace == nil {
		return fmt.Errorf("%w: files root is unavailable", ErrWriteAtomicRenameUnsupported)
	}
	boundary := fs.captureDeleteMountBoundary(fs.workspace.Root())
	if boundary.err != nil {
		return fmt.Errorf(
			"%w: inspect files-root mount boundaries before write: %w",
			ErrWriteAtomicRenameUnsupported,
			boundary.err,
		)
	}

	targetPath := filepath.Clean(fs.workspace.FullPath(name))
	for mountPoint := range boundary.mountPoints {
		if pathWithinMount(targetPath, mountPoint) {
			return fmt.Errorf(
				"%w: write target is inside nested mount %s",
				ErrWriteAtomicRenameUnsupported,
				mountPoint,
			)
		}
	}
	return nil
}

func initializeAtomicWriteRenameProbeFile(probeFile *atomicWriteRenameProbeFile) error {
	if probeFile == nil || probeFile.file == nil {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	if _, err := rand.Read(probeFile.nonce[:]); err != nil {
		return err
	}
	probeFile.hash = sha256.Sum256(probeFile.nonce[:])
	written, err := probeFile.file.Write(probeFile.nonce[:])
	if err != nil {
		return err
	}
	if written != len(probeFile.nonce) {
		return io.ErrShortWrite
	}
	if err := probeFile.file.Sync(); err != nil {
		return err
	}
	return verifyAtomicWriteRenameProbeContent(probeFile.file, probeFile)
}

func rebaselineAtomicWriteRenameProbeFile(
	slot *atomicWriteRenameProbeSlot,
	owner *atomicWriteRenameProbeFile,
) error {
	if slot == nil || slot.root == nil || owner == nil || owner.file == nil {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	rooted, err := rootio.OpenRegularFileNoFollow(slot.root, slot.rel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	defer rooted.Close()

	heldInfo, err := owner.file.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	rootedInfo, err := rooted.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if !heldInfo.Mode().IsRegular() || !rootedInfo.Mode().IsRegular() || !os.SameFile(heldInfo, rootedInfo) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	if err := verifyAtomicWriteRenameProbeContent(owner.file, owner); err != nil {
		return err
	}
	if err := verifyAtomicWriteRenameProbeContent(rooted, owner); err != nil {
		return err
	}

	evidence, err := newAtomicWriteRenameProbeEvidence(rootedInfo)
	if err != nil {
		return err
	}
	heldEvidence, err := newAtomicWriteRenameProbeEvidence(heldInfo)
	if err != nil {
		return err
	}
	if heldEvidence != evidence {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	owner.evidence = evidence
	owner.bound = true
	return verifyAtomicWriteRenameProbeLocation(slot, owner)
}

func newAtomicWriteRenameProbeEvidence(info os.FileInfo) (atomicWriteRenameProbeEvidence, error) {
	if info == nil || !info.Mode().IsRegular() {
		return atomicWriteRenameProbeEvidence{}, errAtomicWriteRenameProbeOwnershipChanged
	}
	persistentIdentity := workspace.PersistentIdentityTokenForFileInfo(info)
	deleteIdentity := workspace.DeleteIdentityTokenForFileInfo(info)
	if persistentIdentity == "" || deleteIdentity == "" {
		return atomicWriteRenameProbeEvidence{}, errors.New("atomic write rename probe identity is unavailable")
	}
	return atomicWriteRenameProbeEvidence{
		persistentIdentity: persistentIdentity,
		deleteIdentity:     deleteIdentity,
		mode:               info.Mode(),
		size:               info.Size(),
		modTime:            info.ModTime(),
	}, nil
}

func atomicWriteRenameProbeEvidenceMatches(expected atomicWriteRenameProbeEvidence, actual os.FileInfo) bool {
	if actual == nil || !actual.Mode().IsRegular() {
		return false
	}
	return expected.persistentIdentity != "" &&
		expected.deleteIdentity != "" &&
		workspace.PersistentIdentityTokenForFileInfo(actual) == expected.persistentIdentity &&
		workspace.DeleteIdentityTokenForFileInfo(actual) == expected.deleteIdentity &&
		actual.Mode() == expected.mode &&
		actual.Size() == expected.size &&
		actual.ModTime().Equal(expected.modTime)
}

func verifyAtomicWriteRenameProbeContent(file *os.File, expected *atomicWriteRenameProbeFile) error {
	if file == nil || expected == nil {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	info, err := file.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len(expected.nonce)) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}

	content := make([]byte, len(expected.nonce)+1)
	read, readErr := file.ReadAt(content, 0)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, readErr)
	}
	if read != len(expected.nonce) ||
		!bytes.Equal(content[:read], expected.nonce[:]) ||
		sha256.Sum256(content[:read]) != expected.hash {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	after, err := file.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if !os.SameFile(info, after) || info.Mode() != after.Mode() || info.Size() != after.Size() ||
		!info.ModTime().Equal(after.ModTime()) ||
		workspace.DeleteIdentityTokenForFileInfo(info) != workspace.DeleteIdentityTokenForFileInfo(after) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return nil
}

func verifyAtomicWriteRenameProbeIsolatedFile(
	expected *atomicWriteRenameProbeFile,
	opened *os.File,
	info os.FileInfo,
) error {
	if expected == nil || expected.file == nil || !expected.bound || opened == nil ||
		info == nil || !info.Mode().IsRegular() {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	openedEvidence, err := newAtomicWriteRenameProbeEvidence(info)
	if err != nil {
		return err
	}
	if openedEvidence.persistentIdentity != expected.evidence.persistentIdentity ||
		openedEvidence.mode != expected.evidence.mode ||
		openedEvidence.size != expected.evidence.size ||
		!openedEvidence.modTime.Equal(expected.evidence.modTime) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}

	openedBefore, err := opened.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	heldBefore, err := expected.file.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	openedBeforeEvidence, err := newAtomicWriteRenameProbeEvidence(openedBefore)
	if err != nil {
		return err
	}
	heldBeforeEvidence, err := newAtomicWriteRenameProbeEvidence(heldBefore)
	if err != nil {
		return err
	}
	if !os.SameFile(info, openedBefore) || !os.SameFile(openedBefore, heldBefore) ||
		openedBeforeEvidence != openedEvidence || heldBeforeEvidence != openedEvidence {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	if err := verifyAtomicWriteRenameProbeContent(opened, expected); err != nil {
		return err
	}
	if err := verifyAtomicWriteRenameProbeContent(expected.file, expected); err != nil {
		return err
	}
	openedAfter, err := opened.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	heldAfter, err := expected.file.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	openedAfterEvidence, err := newAtomicWriteRenameProbeEvidence(openedAfter)
	if err != nil {
		return err
	}
	heldAfterEvidence, err := newAtomicWriteRenameProbeEvidence(heldAfter)
	if err != nil {
		return err
	}
	if !os.SameFile(openedBefore, openedAfter) || !os.SameFile(openedAfter, heldAfter) ||
		openedAfterEvidence != openedEvidence || heldAfterEvidence != openedEvidence {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return nil
}

func verifyAtomicWriteRenameProbeLocation(
	slot *atomicWriteRenameProbeSlot,
	expected *atomicWriteRenameProbeFile,
) error {
	if slot == nil || slot.root == nil || expected == nil || expected.file == nil || !expected.bound {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	lstatInfo, err := slot.root.Lstat(slot.rel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	rooted, err := rootio.OpenRegularFileNoFollow(slot.root, slot.rel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	defer rooted.Close()
	rootedInfo, err := rooted.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	heldInfo, err := expected.file.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if !os.SameFile(lstatInfo, rootedInfo) || !os.SameFile(rootedInfo, heldInfo) ||
		!atomicWriteRenameProbeEvidenceMatches(expected.evidence, lstatInfo) ||
		!atomicWriteRenameProbeEvidenceMatches(expected.evidence, rootedInfo) ||
		!atomicWriteRenameProbeEvidenceMatches(expected.evidence, heldInfo) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	if err := verifyAtomicWriteRenameProbeContent(rooted, expected); err != nil {
		return err
	}
	if err := verifyAtomicWriteRenameProbeContent(expected.file, expected); err != nil {
		return err
	}
	rootedAfter, err := rooted.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	heldAfter, err := expected.file.Stat()
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	current, err := slot.root.Lstat(slot.rel)
	if err != nil {
		return errors.Join(errAtomicWriteRenameProbeOwnershipChanged, err)
	}
	if !os.SameFile(rootedInfo, rootedAfter) || !os.SameFile(rootedAfter, heldAfter) ||
		!os.SameFile(heldAfter, current) ||
		!atomicWriteRenameProbeEvidenceMatches(expected.evidence, rootedAfter) ||
		!atomicWriteRenameProbeEvidenceMatches(expected.evidence, heldAfter) ||
		!atomicWriteRenameProbeEvidenceMatches(expected.evidence, current) {
		return errAtomicWriteRenameProbeOwnershipChanged
	}
	return nil
}

func syncAtomicWriteRenameProbeDir(dir *os.File, label string) error {
	if dir == nil {
		return fmt.Errorf("%s directory is unavailable", label)
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync %s directory: %w", label, err)
	}
	return nil
}
