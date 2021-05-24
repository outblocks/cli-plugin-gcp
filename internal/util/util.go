package util

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"
)

var sanitizeRegex = regexp.MustCompile("[^a-zA-Z0-9-_]+")

func SanitizeName(n string) string {
	return sanitizeRegex.ReplaceAllString(n, "")
}

func SHAString(n string) string {
	h := sha1.New()
	h.Write([]byte(n))
	sha := h.Sum(nil)

	return hex.EncodeToString(sha)
}

func LimitString(n string, lim int) string {
	if len(n) > lim {
		return n[:lim]
	}

	return n
}

func CompareStringPtr(a, b *string) bool {
	if b == nil {
		return true
	}

	return a != nil && *a == *b
}

func CompareIStringPtr(a, b *string) bool {
	if b == nil {
		return true
	}

	return a != nil && strings.EqualFold(*a, *b)
}

func CompareBoolPtr(a, b *bool) bool {
	if b == nil {
		return true
	}

	return a != nil && *a == *b
}
