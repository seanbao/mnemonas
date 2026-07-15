package rootio

import "os"

// CheckedRegularFileVerifier validates the contents of the exact opened file
// that a checked removal isolated. Implementations should verify content and
// stable file metadata before returning nil.
type CheckedRegularFileVerifier func(path string, file *os.File, info os.FileInfo) error
