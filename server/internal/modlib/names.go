package modlib

import (
	"errors"
	"regexp"
	"strings"
)

var (
	jarRe     = regexp.MustCompile(`^[A-Za-z0-9(\[][A-Za-z0-9._+()\[\] -]*\.jar$`)
	archiveRe = regexp.MustCompile(`^[0-9a-f]{32}\.jar$`)

	ErrBadPath = errors.New("bad path")
)

func SafeJarName(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n\"") {
		return "", ErrBadPath
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' })
	for _, p := range parts {
		if p == ".." {
			return "", ErrBadPath
		}
	}
	if len(parts) == 0 {
		return "", ErrBadPath
	}
	name := parts[len(parts)-1]
	if len(name) < 5 || len(name) > 180 || strings.Contains(name, "..") || !jarRe.MatchString(name) {
		return "", ErrBadPath
	}
	return name, nil
}

func SafeArchiveName(name string) bool {
	return archiveRe.MatchString(name)
}
