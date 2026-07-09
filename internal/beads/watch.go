package beads

import (
	"hash/fnv"
	"io/fs"
	"path/filepath"
	"strconv"
)

// Fingerprint folds (path, size, modtime) of every file under <dir>/.beads into
// a single hash. bd's embedded Dolt state lives there and its manifest/journal
// files change on every write, so a changed fingerprint means the issue data
// moved and the board should re-hydrate. A missing .beads yields (0, nil).
func Fingerprint(dir string) (uint64, error) {
	root := filepath.Join(dir, ".beads")
	h := fnv.New64a()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than abort
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		h.Write([]byte(path))
		h.Write([]byte(strconv.FormatInt(info.Size(), 10)))
		h.Write([]byte(strconv.FormatInt(info.ModTime().UnixNano(), 10)))
		return nil
	})
	if err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}
