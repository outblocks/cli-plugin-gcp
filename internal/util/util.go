package util

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"

	"github.com/outblocks/outblocks-plugin-go/types"
)

var sanitizeRegex = regexp.MustCompile("[^a-zA-Z0-9-]+")

func SanitizeName(n string) string {
	return sanitizeRegex.ReplaceAllString(strings.ReplaceAll(n, "_", "-"), "")
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

func PartialMapCompare(m1, m2 map[string]string, keys []string) bool {
	for _, k := range keys {
		v, ok := m1[k]
		if !ok {
			continue
		}

		if m2[k] != v {
			return true
		}
	}

	return false
}

func BoolPtr(b bool) *bool {
	return &b
}

func PlanObject(actions map[string]*types.PlanAction, obj string, planner func() (*types.PlanAction, error)) error {
	action, err := planner()

	if err != nil {
		return err
	}

	if action != nil {
		actions[obj] = action
	}

	return nil
}

type ApplyCallbackFunc func(desc string)

func ApplyObject(actions map[string]*types.PlanAction, obj string, callback func(obj, desc string, progress, total int), applier func(*types.PlanAction, ApplyCallbackFunc) error) error {
	action := actions[obj]

	if action == nil {
		return nil
	}

	progress := 0
	total := action.TotalSteps()

	if total == 0 {
		return nil
	}

	var mu sync.Mutex

	callback(obj, "start", 0, total)

	cb := func(desc string) {
		mu.Lock()
		progress++
		callback(obj, desc, progress, total)
		mu.Unlock()
	}

	err := applier(action, cb)

	return err
}
