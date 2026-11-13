package modlib

import "regexp"

var idRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

func ValidID(s string) bool {
	return idRe.MatchString(s)
}
