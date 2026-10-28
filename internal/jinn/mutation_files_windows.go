package jinn

import "os"

func unlinkRootedFile(root *os.Root, _ *os.File, relative string) error {
	return root.Remove(relative)
}

// Windows does not support fsync on ordinary directory handles. Rooted native
// mutations report their own errors; this adapter adds no directory-flush or
// power-loss durability guarantee. Staged file data is still explicitly synced.
func syncMutationDirectory(_ *os.File) error { return nil }
