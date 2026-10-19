package webtemplate

import "golang.org/x/sys/unix"

func renameProject(from, to string) error { return unix.RenamexNp(from, to, unix.RENAME_EXCL) }
