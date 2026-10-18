// Package idcache keeps looked-up identities in cache/, one small file each.
// A file is named by hashes of the account name and of what the lookup
// depended on, so it holds no token and a changed token finds no file.
package idcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/gperezmz/wakewire/v2/internal/account/application"
	"github.com/gperezmz/wakewire/v2/internal/adapters/atomicfile"
)

// Cache is the identity files of one cache/ folder.
type Cache struct {
	dir string
}

// New returns the cache kept in dir.
func New(dir string) *Cache { return &Cache{dir: dir} }

func short(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func (c *Cache) prefix(account string) string { return "identity-" + short(account) + "-" }

func (c *Cache) path(account, fingerprint string) string {
	return filepath.Join(c.dir, c.prefix(account)+short(fingerprint)+".json")
}

// Get returns the identity cached for the account under the fingerprint.
func (c *Cache) Get(account, fingerprint string) (application.Identity, bool) {
	data, err := os.ReadFile(c.path(account, fingerprint))
	if err != nil {
		return application.Identity{}, false
	}
	var id application.Identity
	if json.Unmarshal(data, &id) != nil || id.Login == "" {
		return application.Identity{}, false
	}
	return id, true
}

// Put caches the identity, dropping what the account had cached before: only
// the latest token's answer is worth keeping.
func (c *Cache) Put(account, fingerprint string, id application.Identity) error {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	if err := c.Forget(account); err != nil {
		return err
	}
	data, err := json.Marshal(id)
	if err != nil {
		return err
	}
	return atomicfile.Write(c.path(account, fingerprint), data, 0o600)
}

// Forget drops everything cached for the account.
func (c *Cache) Forget(account string) error {
	files, err := filepath.Glob(filepath.Join(c.dir, c.prefix(account)+"*.json"))
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
