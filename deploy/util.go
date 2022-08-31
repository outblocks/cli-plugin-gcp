package deploy

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"

	"github.com/outblocks/outblocks-plugin-go/registry"
	plugin_util "github.com/outblocks/outblocks-plugin-go/util"
)

var (
	_ registry.ResourceDiffCalculator = (*CacheInvalidate)(nil)
)

func hashFile(f string) (string, error) {
	file, err := os.Open(f)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buf := make([]byte, 30*1024)
	hash := md5.New()

	for {
		n, err := file.Read(buf)
		if n > 0 {
			_, err := hash.Write(buf[:n])
			if err != nil {
				return "", err
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return "", err
		}
	}

	sum := hash.Sum(nil)

	return hex.EncodeToString(sum), nil
}

func findFiles(root string, patterns []string) (ret map[string]string, err error) {
	ret = make(map[string]string)

	err = plugin_util.WalkWithExclusions(root, patterns, func(path, rel string, info os.FileInfo) error {
		if info.IsDir() {
			return nil
		}

		hash, err := hashFile(path)
		if err != nil {
			return err
		}

		ret[rel] = hash

		return nil
	})

	return ret, err
}
