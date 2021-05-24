package deploy

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

type FileInfo struct {
	Hash string `json:"hash"`
}

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

func findFiles(root string) (ret map[string]*FileInfo, err error) {
	ret = make(map[string]*FileInfo)

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

			ret[rel] = &FileInfo{
				Hash: hash,
			}

			return nil
		})

	return ret, err
}

func diffFiles(cur, dest map[string]*FileInfo) (add, update, del map[string]*FileInfo) {
	if cur == nil {
		// Add all.
		return dest, nil, nil
	}

	if dest == nil {
		// Delete all.
		return nil, nil, cur
	}

	add = make(map[string]*FileInfo)
	update = make(map[string]*FileInfo)
	del = make(map[string]*FileInfo)

	for name, f := range dest {
		if c, ok := cur[name]; ok {
			delete(cur, name)

			if c.Hash != f.Hash {
				update[name] = f
			}
		} else {
			add[name] = f
		}
	}

	for name, f := range cur {
		del[name] = f
	}

	return add, update, del
}
