package main

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

type APIKeyRecord struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	RateLimit int    `json:"rate_limit"` // requests per minute, 0 = unlimited
	CreatedAt string `json:"created_at"`
	LastUsed  string `json:"last_used"`
}

type KeyManager struct {
	mu        sync.RWMutex
	keys      map[string]*APIKeyRecord
	keysFile  string
	modTime   time.Time
}

func genAPIKey(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func NewKeyManager(keysFile string) *KeyManager {
	return &KeyManager{
		keys:     make(map[string]*APIKeyRecord),
		keysFile: keysFile,
	}
}

func (km *KeyManager) Load() {
	km.mu.Lock()
	defer km.mu.Unlock()
	km.loadLocked()
}

func (km *KeyManager) loadLocked() {
	f, err := os.Open(km.keysFile)
	if err != nil {
		if os.IsNotExist(err) {
			km.bootstrapLocked()
		}
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return
	}
	km.modTime = fi.ModTime()

	loaded := make(map[string]*APIKeyRecord)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var k APIKeyRecord
		if err := json.Unmarshal([]byte(line), &k); err != nil {
			continue
		}
		if k.Key == "" || !k.Enabled {
			continue
		}
		if k.RateLimit == 0 {
			k.RateLimit = 60
		}
		loaded[k.Key] = &k
	}
	km.keys = loaded

	if len(km.keys) == 0 {
		km.bootstrapLocked()
	}
	log.Info("keymgr: loaded", zap.Int("count", len(km.keys)))
}

func (km *KeyManager) bootstrapLocked() {
	key := genAPIKey("sk-")
	km.keys[key] = &APIKeyRecord{
		Key:       key,
		Name:      "default",
		Enabled:   true,
		RateLimit: 60,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	km.writeLocked()
	log.Warn("keymgr: no keys found, generated default key", zap.String("key", key[:16]+"..."))
}

func (km *KeyManager) writeLocked() {
	f, err := os.Create(km.keysFile)
	if err != nil {
		return
	}
	defer f.Close()
	for _, k := range km.keys {
		b, _ := json.Marshal(k)
		f.Write(b)
		f.Write([]byte("\n"))
	}
}

func (km *KeyManager) Validate(key string) (*APIKeyRecord, bool) {
	km.mu.RLock()
	defer km.mu.RUnlock()
	for _, k := range km.keys {
		if subtle.ConstantTimeCompare([]byte(k.Key), []byte(key)) == 1 {
			return k, true
		}
	}
	return nil, false
}

func (km *KeyManager) ReloadIfNeeded() {
	fi, err := os.Stat(km.keysFile)
	if err != nil {
		return
	}
	km.mu.RLock()
	stale := !fi.ModTime().Equal(km.modTime)
	km.mu.RUnlock()
	if stale {
		km.Load()
	}
}

func (km *KeyManager) AllKeys() []*APIKeyRecord {
	km.mu.RLock()
	defer km.mu.RUnlock()
	out := make([]*APIKeyRecord, 0, len(km.keys))
	for _, k := range km.keys {
		out = append(out, k)
	}
	return out
}

func (km *KeyManager) TouchKey(key string) {
	km.mu.Lock()
	defer km.mu.Unlock()
	for _, k := range km.keys {
		if k.Key == key {
			k.LastUsed = time.Now().Format("2006-01-02 15:04:05")
			return
		}
	}
}

func (km *KeyManager) CreateKey(name string) *APIKeyRecord {
	km.mu.Lock()
	defer km.mu.Unlock()
	k := &APIKeyRecord{
		Key:       genAPIKey("sk-"),
		Name:      name,
		Enabled:   true,
		RateLimit: 60,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	km.keys[k.Key] = k
	km.writeLocked()
	return k
}

func (km *KeyManager) FindByMasked(masked string) string {
	km.mu.RLock()
	defer km.mu.RUnlock()
	// 脱敏格式: sk-前缀...后缀 (例如 sk-abcd1234...wxyz)
	parts := strings.SplitN(masked, "...", 2)
	if len(parts) != 2 || len(parts[0]) < 8 || len(parts[1]) < 4 {
		return masked // 不是脱敏格式, 直接返回原文
	}
	prefix := parts[0]
	suffix := parts[1]
	for _, k := range km.keys {
		if len(k.Key) >= len(prefix)+len(suffix) &&
			k.Key[:len(prefix)] == prefix &&
			k.Key[len(k.Key)-len(suffix):] == suffix {
			return k.Key
		}
	}
	return masked
}

func (km *KeyManager) DeleteKey(key string) bool {
	km.mu.Lock()
	defer km.mu.Unlock()
	if _, ok := km.keys[key]; !ok {
		return false
	}
	delete(km.keys, key)
	km.writeLocked()
	return true
}

func (km *KeyManager) ToggleKey(key string) *APIKeyRecord {
	km.mu.Lock()
	defer km.mu.Unlock()
	k, ok := km.keys[key]
	if !ok {
		return nil
	}
	k.Enabled = !k.Enabled
	km.writeLocked()
	return k
}
