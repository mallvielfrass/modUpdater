package mods

import (
	"os"

	"modupdater/internal/modlib"
)

type Meta = modlib.Meta

func validSlug(s string) bool {
	return modlib.ValidSlug(s)
}

func ReadMeta(f *os.File, filename string) (Meta, error) {
	return modlib.ReadMeta(f, filename)
}

func metaFromPath(path, filename string) (Meta, error) {
	return modlib.MetaFromPath(path, filename)
}
