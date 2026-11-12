// Package vault keeps tuon's secrets encrypted on disk.
//
// One random data key encrypts everything sensitive (the session, message
// text, keys). The data key itself is stored wrapped:
//   - on a Mac, by a Secure Enclave key: unwrapping it needs Touch ID or the
//     Mac password, and the private key never leaves the chip;
//   - elsewhere (or if the Mac has no Secure Enclave), by a passphrase,
//     stretched with Argon2id.
//
// A copy of tuon's folder is useless without that unlock.
package vault

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ralph/tuon/internal/safefile"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	KindEnclave    = "secure-enclave"
	KindPassphrase = "passphrase"
	fileName       = "vault.json"
	prefix         = "v1:" // marks a sealed string in the database
)

// ErrNotConfirmed: Touch ID / password was cancelled or failed.
var ErrNotConfirmed = errors.New("unlock wasn't confirmed")

// ErrWrongPassphrase: the passphrase didn't open the vault.
var ErrWrongPassphrase = errors.New("wrong passphrase")

// Vault holds the unwrapped data key while tuon runs.
type Vault struct {
	key  [chacha20poly1305.KeySize]byte
	kind string
	file file
	dir  string
}

type file struct {
	Kind    string `json:"kind"`
	Wrapped string `json:"wrapped"` // data key, sealed with the key-encryption key
	// secure enclave
	SEKey  string `json:"se_key,omitempty"`  // the chip's wrapped private key (useless elsewhere)
	SEPeer string `json:"se_peer,omitempty"` // our one-time public key for the key agreement
	// passphrase
	Salt    string `json:"salt,omitempty"`
	Time    uint32 `json:"time,omitempty"`
	Memory  uint32 `json:"memory,omitempty"` // KiB
	Threads uint8  `json:"threads,omitempty"`
}

// Exists reports whether tuon's folder has a vault.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, fileName))
	return err == nil
}

// Kind says how an existing vault unlocks ("" if there is none).
func Kind(dir string) string {
	f, err := readFile(dir)
	if err != nil {
		return ""
	}
	return f.Kind
}

// Enclave is the Secure Enclave side (the Swift helper on macOS).
type Enclave interface {
	Create() (key, pub []byte, err error)
	Derive(key, peer []byte) ([]byte, error)
}

// CreateEnclave makes a new vault guarded by the Secure Enclave. It needs no
// Touch ID: the wrap uses the chip's public key only.
func CreateEnclave(dir string, se Enclave) (*Vault, error) {
	seKey, sePub, err := se.Create()
	if err != nil {
		return nil, err
	}
	pub, err := ecdh.P256().NewPublicKey(sePub)
	if err != nil {
		return nil, fmt.Errorf("enclave public key: %w", err)
	}
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return nil, err
	}
	v := &Vault{kind: KindEnclave, dir: dir}
	if _, err := rand.Read(v.key[:]); err != nil {
		return nil, err
	}
	kek, err := enclaveKEK(shared)
	if err != nil {
		return nil, err
	}
	v.file = file{Kind: KindEnclave, SEKey: b64(seKey), SEPeer: b64(eph.PublicKey().Bytes())}
	if v.file.Wrapped, err = wrap(kek, v.key[:]); err != nil {
		return nil, err
	}
	return v, v.save()
}

// MinPassphrase is the shortest passphrase tuon accepts (Ralph's choice,
// 2026-11-12: 6). Against a copied vault.json, Argon2id slows guessing, but
// only length and randomness really protect it.
const MinPassphrase = 6

// CreatePassphrase makes a new vault guarded by a passphrase.
func CreatePassphrase(dir, pass string) (*Vault, error) {
	if len([]rune(pass)) < MinPassphrase {
		return nil, fmt.Errorf("use at least %d characters", MinPassphrase)
	}
	v := &Vault{kind: KindPassphrase, dir: dir}
	if _, err := rand.Read(v.key[:]); err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	v.file = file{Kind: KindPassphrase, Salt: b64(salt), Time: 3, Memory: 64 * 1024, Threads: 4}
	var err error
	kek, err := v.file.passKEK(pass)
	if err != nil {
		return nil, err
	}
	if v.file.Wrapped, err = wrap(kek, v.key[:]); err != nil {
		return nil, err
	}
	return v, v.save()
}

// UnlockEnclave opens the vault with Touch ID / the Mac password.
func UnlockEnclave(dir string, se Enclave) (*Vault, error) {
	f, err := readFile(dir)
	if err != nil {
		return nil, err
	}
	if f.Kind != KindEnclave {
		return nil, fmt.Errorf("this vault unlocks with a %s", f.Kind)
	}
	seKey, err1 := unb64(f.SEKey)
	peer, err2 := unb64(f.SEPeer)
	if err := errors.Join(err1, err2); err != nil {
		return nil, fmt.Errorf("vault file damaged: %w", err)
	}
	shared, err := se.Derive(seKey, peer)
	if err != nil {
		return nil, err
	}
	kek, err := enclaveKEK(shared)
	if err != nil {
		return nil, err
	}
	return open(dir, f, kek)
}

// UnlockPassphrase opens a passphrase vault.
func UnlockPassphrase(dir, pass string) (*Vault, error) {
	f, err := readFile(dir)
	if err != nil {
		return nil, err
	}
	if f.Kind != KindPassphrase {
		return nil, fmt.Errorf("this vault unlocks with %s", f.Kind)
	}
	kek, err := f.passKEK(pass)
	if err != nil {
		return nil, err
	}
	v, err := open(dir, f, kek)
	if err != nil {
		return nil, ErrWrongPassphrase
	}
	return v, nil
}

// CheckPassphrase reports whether pass opens this (already unlocked) vault,
// for the idle-lock screen.
func (v *Vault) CheckPassphrase(pass string) bool {
	if v.kind != KindPassphrase {
		return false // a Touch ID vault has no passphrase (and no Argon2 settings to run)
	}
	kek, err := v.file.passKEK(pass)
	if err != nil {
		return false
	}
	_, err = open(v.dir, v.file, kek)
	return err == nil
}

// Handoff gives the data key, to pass to tuon's own sandboxed child over a
// private pipe (so you unlock once, not once per process). Never write it
// anywhere else.
func (v *Vault) Handoff() []byte { return append([]byte(nil), v.key[:]...) }

// Adopt opens the vault with a key handed over by the launcher.
func Adopt(dir string, key []byte) (*Vault, error) {
	f, err := readFile(dir)
	if err != nil {
		return nil, err
	}
	if len(key) != chacha20poly1305.KeySize {
		return nil, errors.New("handed-over key has the wrong size")
	}
	v := &Vault{kind: f.Kind, file: f, dir: dir}
	copy(v.key[:], key)
	return v, nil
}

// DeriveKey gives a separate 32-byte key for one purpose (e.g. keys.db),
// so the data key itself is never handed out.
func (v *Vault) DeriveKey(purpose string) []byte {
	k, err := hkdf.Key(sha256.New, v.key[:], nil, "tuon "+purpose, 32)
	if err != nil {
		panic(err) // fixed-size HKDF can't fail
	}
	return k
}

// Kind says how this vault unlocks.
func (v *Vault) Kind() string { return v.kind }

// Seal encrypts b; each call uses a fresh random nonce.
func (v *Vault) Seal(b []byte) []byte {
	aead, _ := chacha20poly1305.NewX(v.key[:])
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(b)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // the system's random source failing is not recoverable
	}
	return aead.Seal(nonce, nonce, b, nil)
}

// Open decrypts what Seal made.
func (v *Vault) Open(b []byte) ([]byte, error) {
	aead, _ := chacha20poly1305.NewX(v.key[:])
	if len(b) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("sealed data too short")
	}
	return aead.Open(nil, b[:aead.NonceSize()], b[aead.NonceSize():], nil)
}

// SealString seals text for a database column ("v1:" + base64). Empty
// stays empty, so "no text" stays cheap to test for.
func (v *Vault) SealString(s string) string {
	if s == "" {
		return ""
	}
	return prefix + b64(v.Seal([]byte(s)))
}

// OpenString reverses SealString. Text without the prefix is from before
// encryption was turned on and passes through unchanged.
func (v *Vault) OpenString(s string) (string, error) {
	if !strings.HasPrefix(s, prefix) {
		return s, nil
	}
	raw, err := unb64(s[len(prefix):])
	if err != nil {
		return "", err
	}
	b, err := v.Open(raw)
	return string(b), err
}

// Sealed reports whether a stored string is already encrypted.
func Sealed(s string) bool { return strings.HasPrefix(s, prefix) }

// IsSealed is Sealed as a method, for the store's Cipher interface.
func (v *Vault) IsSealed(s string) bool { return Sealed(s) }

// Remove deletes the vault file (logout --wipe).
func Remove(dir string) error {
	err := os.Remove(filepath.Join(dir, fileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ---- internals ----

func enclaveKEK(shared []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, shared, nil, "tuon vault v1", chacha20poly1305.KeySize)
}

// passKEK stretches the passphrase with vault.json's Argon2 settings,
// within sane bounds: vault.json is in a folder the sandboxed app can
// write, and settings like 4 TiB of memory or 0 threads would crash the
// launcher that unlocks it.
func (f file) passKEK(pass string) ([]byte, error) {
	if f.Time < 1 || f.Time > 10 || f.Memory < 8*1024 || f.Memory > 1024*1024 || f.Threads < 1 || f.Threads > 16 {
		return nil, ErrBadSettings
	}
	salt, err := unb64(f.Salt)
	if err != nil || len(salt) < 16 {
		return nil, ErrBadSettings
	}
	return argon2.IDKey([]byte(pass), salt, f.Time, f.Memory, f.Threads, chacha20poly1305.KeySize), nil
}

// ErrBadSettings: vault.json's passphrase settings are outside what tuon
// ever writes, so they were changed by something else.
var ErrBadSettings = errors.New("vault.json has passphrase settings tuon never writes: it was changed by another program")

func wrap(kek, key []byte) (string, error) {
	aead, err := chacha20poly1305.NewX(kek)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return b64(aead.Seal(nonce, nonce, key, []byte("tuon data key"))), nil
}

func open(dir string, f file, kek []byte) (*Vault, error) {
	aead, err := chacha20poly1305.NewX(kek)
	if err != nil {
		return nil, err
	}
	raw, err := unb64(f.Wrapped)
	if err != nil || len(raw) < aead.NonceSize() {
		return nil, errors.New("vault file damaged")
	}
	key, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte("tuon data key"))
	if err != nil || len(key) != chacha20poly1305.KeySize {
		return nil, errors.New("couldn't open the vault")
	}
	v := &Vault{kind: f.Kind, file: f, dir: dir}
	copy(v.key[:], key)
	return v, nil
}

func (v *Vault) save() error {
	b, err := json.MarshalIndent(v.file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return err
	}
	// on disk before the rename, and the rename on disk before returning:
	// a crash must never leave a half-written vault.json (no key = no data)
	tmp := filepath.Join(v.dir, fileName+".tmp")
	if err := safefile.WriteFile(tmp, b, 0o600); err != nil { // synced, never through a link
		return err
	}
	if err := os.Rename(tmp, filepath.Join(v.dir, fileName)); err != nil {
		return err
	}
	if d, err := os.Open(v.dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func readFile(dir string) (file, error) {
	var f file
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("vault file damaged: %w", err)
	}
	return f, nil
}

func b64(b []byte) string            { return base64.StdEncoding.EncodeToString(b) }
func unb64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
