package deploy

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
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

func findFiles(root string) (ret map[string]string, err error) {
	ret = make(map[string]string)

	err = filepath.Walk(root,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
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
