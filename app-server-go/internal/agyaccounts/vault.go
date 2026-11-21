package agyaccounts

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"

	"astrorder.dev/contracts/agycredential"
)

var (
	ErrInvalidCredential = errors.New("请选择有效的 AGY Token JSON 文件（最大 512 KB）")
	ErrNotFound          = errors.New("找不到所选 AGY 账号")
	accountIDPattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// MaxImportBytes also accommodates snapshots from the old per-machine account
// store, where the original token was base64-encoded inside a JSON record.
const MaxImportBytes = 1024 * 1024
const maxRecordBytes = 2 * agycredential.MaxBytes

type Entry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Identity string `json:"-"`
}

type record struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token []byte `json:"token"`
}

// Vault is the App's library, independent of the selected AGY target.
type Vault struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) *Vault { return &Vault{dir: dir} }

func ImportCredential(data []byte) ([]byte, error) {
	if _, valid := agycredential.Identity(data); valid {
		return data, nil
	}
	if len(data) > MaxImportBytes {
		return nil, ErrInvalidCredential
	}
	var snapshot struct {
		Name  string `json:"name"`
		Token []byte `json:"token"`
	}
	if json.Unmarshal(data, &snapshot) != nil || strings.TrimSpace(snapshot.Name) == "" {
		return nil, ErrInvalidCredential
	}
	if _, valid := agycredential.Identity(snapshot.Token); !valid {
		return nil, ErrInvalidCredential
	}
	return snapshot.Token, nil
}

func (v *Vault) List() ([]Entry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.list()
}

func (v *Vault) list() ([]Entry, error) {
	items := []Entry{}
	if err := v.checkDir(); errors.Is(err, os.ErrNotExist) {
		return items, nil
	} else if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(v.dir)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		id, ok := strings.CutSuffix(file.Name(), ".sealed")
		if !ok || !accountIDPattern.MatchString(id) {
			continue
		}
		rec, identity, err := v.load(id)
		if err != nil {
			return nil, fmt.Errorf("read stored AGY account: %w", err)
		}
		items = append(items, Entry{ID: id, Name: rec.Name, Identity: identity})
	}
	slices.SortStableFunc(items, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	return items, nil
}

func (v *Vault) Credential(id string) ([]byte, error) {
	if !accountIDPattern.MatchString(id) {
		return nil, ErrNotFound
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.checkDir(); errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	rec, _, err := v.load(id)
	return rec.Token, err
}

func (v *Vault) Save(name string, token []byte) (string, error) {
	identity, valid := agycredential.Identity(token)
	if !valid {
		return "", ErrInvalidCredential
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	items, err := v.list()
	if err != nil {
		return "", err
	}
	id := ""
	for _, entry := range items {
		if entry.Identity == identity {
			id = entry.ID
			break
		}
	}
	if id == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		id = hex.EncodeToString(random[:])
	}
	if err := os.Mkdir(v.dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	if err := v.checkDir(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(record{ID: id, Name: name, Token: token})
	if err != nil {
		return "", err
	}
	sealed, err := v.seal(raw, len(items) == 0)
	if err != nil {
		return "", err
	}
	path := filepath.Join(v.dir, id+".sealed")
	err = writePrivate(v.dir, sealed, func(temp string) error { return os.Rename(temp, path) })
	return id, err
}

func (v *Vault) checkDir() error {
	info, err := os.Lstat(v.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return errors.New("invalid AGY vault directory permissions")
	}
	return nil
}

func (v *Vault) load(id string) (record, string, error) {
	var rec record
	blob, err := readPrivate(filepath.Join(v.dir, id+".sealed"), maxRecordBytes)
	if errors.Is(err, os.ErrNotExist) {
		return rec, "", ErrNotFound
	}
	if err != nil {
		return rec, "", err
	}
	raw, err := v.unseal(blob)
	if err != nil {
		return rec, "", err
	}
	if json.Unmarshal(raw, &rec) != nil || rec.ID != id || rec.Name == "" {
		return record{}, "", errors.New("invalid AGY vault record")
	}
	identity, ok := agycredential.Identity(rec.Token)
	if !ok {
		return record{}, "", ErrInvalidCredential
	}
	return rec, identity, nil
}

func readPrivate(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("invalid AGY vault file permissions or size")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("AGY vault file too large")
	}
	return data, err
}

func writePrivate(dir string, data []byte, publish func(string) error) error {
	file, err := os.CreateTemp(dir, ".agy-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return publish(file.Name())
}
